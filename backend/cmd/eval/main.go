// Command eval measures segment extraction on the 25-brief test set
// (make eval, D-TEST). It seeds a temporary database with a fixed dataset
// and date, sends each brief to the model several times through the
// production pipeline, scores the runs and writes a Markdown and a JSON
// report. With -dry-run it uses ideal replies and makes no API call.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/eval"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// seedNow is the fixed "today" of the eval dataset: the "last N days"
// windows, and so the expected audiences, never drift.
var seedNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run() error {
	runs := flag.Int("runs", 3, "runs per brief")
	only := flag.String("cases", "", "comma-separated case ids (default: all)")
	parallel := flag.Int("parallel", 3, "runs in flight at once")
	maxCalls := flag.Int64("max-calls", 0, "stop after this many API calls (default: runs × briefs × 3, the worst case with 2 repairs)")
	dryRun := flag.Bool("dry-run", false, "use ideal replies instead of the API (free)")
	out := flag.String("out", "", "directory for the reports (default: print the summary only)")
	commit := flag.String("commit", "unknown", "git commit shown in the report")
	flag.Parse()

	cases, err := eval.LoadCases()
	if err != nil {
		return err
	}
	if cases, err = filterCases(cases, *only); err != nil {
		return err
	}
	if *maxCalls == 0 {
		*maxCalls = int64(*runs * len(cases) * (1 + llm.MaxRepairs))
	}

	cfg, err := platform.Load()
	if err != nil {
		return err
	}

	var client llm.Client
	provider, model := "anthropic", cfg.LLMModel
	if *dryRun {
		provider, model = "dry-run", "ideal-replies"
		client = eval.NewIdealClient(cases)
	} else {
		if cfg.AnthropicAPIKey == "" {
			return errors.New("ANTHROPIC_API_KEY is not set (in the environment or .env); use -dry-run to run without the API")
		}
		// No SDK retries: every request is counted by the budget, so the
		// call count in the report is exact. A failed call is reported as
		// an API error rather than silently repeated.
		noRetries := 0
		client = llm.NewAnthropic(llm.AnthropicConfig{
			APIKey: cfg.AnthropicAPIKey, Model: cfg.LLMModel, BaseURL: cfg.AnthropicBaseURL, MaxRetries: &noRetries,
		})
	}
	budget := &eval.BudgetClient{Client: client, Max: *maxCalls}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Hazırlanıyor: geçici veritabanı, seed (%d kullanıcı, \"bugün\" = %s)\n",
		seed.DefaultUserCount, seedNow.Format("2006-01-02"))
	pool, drop, err := platform.CreateTempDB(ctx, cfg.DatabaseURL, "scc_eval")
	if err != nil {
		return fmt.Errorf("%w (is `make db-up` running?)", err)
	}
	defer func() {
		if err := drop(); err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
		}
	}()
	if err := platform.Migrate(ctx, pool); err != nil {
		return err
	}
	if err := seed.Insert(ctx, pool, seed.Generate(seed.DefaultSeed, seed.DefaultUserCount, seedNow)); err != nil {
		return err
	}

	fmt.Printf("Başlıyor: %s (%s) · %d brief × %d çalıştırma · en fazla %d çağrı\n",
		model, provider, len(cases), *runs, *maxCalls)
	meta := eval.Meta{
		Model: model, Provider: provider, Effort: string(llm.EffortLow), StartedAt: time.Now(),
		Commit: *commit, Runs: *runs, SeedNow: seedNow, SeedUsers: seed.DefaultUserCount, RNGSeed: seed.DefaultSeed,
	}
	results, err := eval.Execute(ctx, cases, eval.Options{
		Client: budget, Audience: eval.DBAudience(pool, seedNow), Today: seedNow, Runs: *runs, Parallel: *parallel,
		Progress: func(c eval.Case, run int, r eval.Run, v eval.Verdict) {
			mark := "✅"
			if !v.Correct {
				mark = "❌"
			}
			line := fmt.Sprintf("  #%-2d çalıştırma %d  %s %s", c.ID, run, mark, r.Status)
			if r.Audience != nil {
				line += fmt.Sprintf(" · %d kişi", len(r.Audience))
			}
			if r.Repairs > 0 {
				line += fmt.Sprintf(" · %d onarım", r.Repairs)
			}
			line += fmt.Sprintf(" · %.1f sn", r.Duration.Seconds())
			if !v.Correct {
				line += " · " + strings.Join(v.Problems, "; ")
			}
			fmt.Println(line)
		},
	})
	meta.Calls = budget.Calls()
	if err != nil {
		return fmt.Errorf("%w (%d calls made)", err, meta.Calls)
	}
	meta.Duration = eval.Duration(time.Since(meta.StartedAt))

	fmt.Println()
	fmt.Print(eval.TerminalSummary(meta, results))
	if *out != "" {
		paths, err := writeReports(*out, meta, results)
		if err != nil {
			return err
		}
		fmt.Println("Rapor:      ", strings.Join(paths, ", "))
	}
	if t := eval.Summarize(results); t.Unsafe > 0 {
		return fmt.Errorf("%d runs failed the SQL safety check", t.Unsafe)
	}
	return nil
}

func filterCases(cases []eval.Case, only string) ([]eval.Case, error) {
	if only == "" {
		return cases, nil
	}
	byID := map[int]eval.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	var picked []eval.Case
	for f := range strings.SplitSeq(only, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("-cases: %q is not a number", f)
		}
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("-cases: no case %d", id)
		}
		picked = append(picked, c)
	}
	return picked, nil
}

func writeReports(dir string, meta eval.Meta, results []eval.CaseResult) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := filepath.Join(dir, meta.StartedAt.Local().Format("2006-01-02T1504")+"-"+meta.Model)
	md := base + ".md"
	if err := os.WriteFile(md, []byte(eval.Markdown(meta, results)), 0o644); err != nil {
		return nil, err
	}
	js, err := eval.JSON(meta, results)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(base+".json", append(js, '\n'), 0o644); err != nil {
		return nil, err
	}
	return []string{md, base + ".json"}, nil
}
