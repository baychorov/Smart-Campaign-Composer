package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// ErrUnsafeSQL marks a compiled query that failed the safety check. The
// run is scored as wrong and make eval exits with an error.
var ErrUnsafeSQL = errors.New("compiled SQL failed the safety check")

// ErrBudget is returned once more calls are attempted than the budget
// allows. The eval stops instead of spending more.
var ErrBudget = errors.New("eval call budget exhausted")

// AudienceFunc returns the sorted ids of the users an approvable rule
// selects (never nil, possibly empty).
type AudienceFunc func(ctx context.Context, r segment.Rule) ([]int64, error)

// Options configures Execute.
type Options struct {
	Client   llm.Client
	Audience AudienceFunc
	// Today is the date the model is told, so relative dates in briefs
	// ("Ekim'de") resolve against the seed's fixed "now", not the clock.
	Today    time.Time
	Runs     int // runs per brief
	Parallel int // runs in flight at once
	// Timeout bounds one run, repairs included. Zero means 3 minutes.
	Timeout time.Duration
	// Progress, if set, is called after each finished run.
	Progress func(c Case, run int, r Run, v Verdict)
}

// CaseResult holds every run of one brief.
type CaseResult struct {
	Case     Case
	Expected [][]int64 // audience of each expected rule
	Runs     []Run
	Verdicts []Verdict
}

// CorrectRuns counts the correct runs.
func (cr CaseResult) CorrectRuns() int {
	n := 0
	for _, v := range cr.Verdicts {
		if v.Correct {
			n++
		}
	}
	return n
}

// Execute computes the expected audiences, then runs every brief opts.Runs
// times through llm.ExtractSegment and scores each run. A model failure is
// a scored result; an infrastructure failure (database, budget) aborts.
func Execute(ctx context.Context, cases []Case, opts Options) ([]CaseResult, error) {
	if opts.Runs < 1 || opts.Parallel < 1 {
		return nil, fmt.Errorf("runs and parallel must be at least 1")
	}
	if opts.Today.IsZero() {
		return nil, fmt.Errorf("today must be set")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Minute
	}

	results := make([]CaseResult, len(cases))
	for i, c := range cases {
		results[i] = CaseResult{Case: c, Runs: make([]Run, opts.Runs), Verdicts: make([]Verdict, opts.Runs)}
		for _, r := range c.Rules {
			ids, err := opts.Audience(ctx, r)
			if err != nil {
				return nil, fmt.Errorf("case %d: expected audience: %w", c.ID, err)
			}
			results[i].Expected = append(results[i].Expected, ids)
		}
	}

	type job struct{ caseIdx, run int }
	jobs := make(chan job)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var wg sync.WaitGroup
	var mu sync.Mutex // guards Progress calls
	for range opts.Parallel {
		wg.Go(func() {
			for j := range jobs {
				res := &results[j.caseIdx]
				r, err := runOnce(ctx, opts, res.Case)
				if err != nil {
					cancel(fmt.Errorf("case %d run %d: %w", res.Case.ID, j.run+1, err))
					continue
				}
				v := Score(res.Case, res.Expected, r)
				res.Runs[j.run], res.Verdicts[j.run] = r, v
				if opts.Progress != nil {
					mu.Lock()
					opts.Progress(res.Case, j.run+1, r, v)
					mu.Unlock()
				}
			}
		})
	}
	// Run 1 of every brief first, then run 2, ...: an early stop still
	// leaves a usable first pass over the whole set.
feed:
	for run := range opts.Runs {
		for i := range cases {
			select {
			case jobs <- job{i, run}:
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(jobs)
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

// runOnce sends one brief through the production extraction pipeline.
func runOnce(ctx context.Context, opts Options, c Case) (Run, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	start := time.Now()
	res, err := llm.ExtractSegment(ctx, opts.Client, c.Brief, opts.Today)
	usage := res.Usage()
	r := Run{
		Repairs: res.Repairs, Calls: len(res.Attempts),
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		Duration: time.Since(start),
	}

	var refusal *llm.RefusalError
	switch {
	case errors.Is(err, ErrBudget):
		return r, err
	case err == nil:
		rule := res.Validation.Rule
		r.Rule, r.Status, r.SendAt = &rule, string(rule.Status), res.SendAt
		if !res.Validation.Approvable() {
			return r, nil
		}
		ids, err := opts.Audience(ctx, rule)
		switch {
		case errors.Is(err, ErrUnsafeSQL):
			r.Unsafe = err.Error()
		case err != nil:
			return r, fmt.Errorf("audience: %w", err)
		}
		r.Audience = ids
	case errors.Is(err, llm.ErrInvalidOutput):
		r.Status = StatusInvalid
		codes := make([]string, len(res.Issues))
		for i, is := range res.Issues {
			codes[i] = is.Code
		}
		r.Error = "son hatalar: " + strings.Join(codes, ", ")
	case errors.As(err, &refusal):
		r.Status, r.Error = StatusRefusal, "category "+refusal.Refusal.Category
	default:
		r.Status, r.Error = StatusError, err.Error()
	}
	return r, nil
}

// BudgetClient stops the eval after Max calls, counting repairs and
// retries, so a run never spends more than announced.
type BudgetClient struct {
	Client llm.Client
	Max    int64
	calls  atomic.Int64
}

// Stream implements llm.Client.
func (b *BudgetClient) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	if b.calls.Add(1) > b.Max {
		return llm.Response{}, ErrBudget
	}
	return b.Client.Stream(ctx, req, onDelta)
}

// Calls returns how many calls were let through.
func (b *BudgetClient) Calls() int64 { return min(b.calls.Load(), b.Max) }
