package eval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Meta describes how an eval was run; it heads every report.
type Meta struct {
	Model     string    `json:"model"`
	Provider  string    `json:"provider"` // "anthropic", or "dry-run" (ideal replies, no API)
	Effort    string    `json:"effort"`
	StartedAt time.Time `json:"started_at"`
	Duration  Duration  `json:"duration"`
	Commit    string    `json:"commit"`
	Runs      int       `json:"runs_per_brief"`
	Calls     int64     `json:"calls"`
	SeedNow   time.Time `json:"seed_now"`
	SeedUsers int       `json:"seed_users"`
	RNGSeed   uint64    `json:"rng_seed"`
}

// Duration marshals as text such as "1m23s".
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).Round(time.Millisecond).String())
}

// Totals are the headline numbers.
type Totals struct {
	Briefs           int           `json:"briefs"`
	Runs             int           `json:"runs"`
	CorrectRuns      int           `json:"correct_runs"`
	ConsistentBriefs int           `json:"consistent_briefs"`
	RunsWithRepairs  int           `json:"runs_with_repairs"`
	Repairs          int           `json:"repairs"`
	Errors           int           `json:"errors"`
	Unsafe           int           `json:"unsafe"`
	InputTokens      int64         `json:"input_tokens"`
	OutputTokens     int64         `json:"output_tokens"`
	AvgRunDuration   time.Duration `json:"-"`
}

// Summarize computes the totals of a finished eval.
func Summarize(results []CaseResult) Totals {
	var t Totals
	var total time.Duration
	for _, cr := range results {
		t.Briefs++
		t.CorrectRuns += cr.CorrectRuns()
		if Consistent(cr.Runs) {
			t.ConsistentBriefs++
		}
		for _, r := range cr.Runs {
			t.Runs++
			t.Repairs += r.Repairs
			if r.Repairs > 0 {
				t.RunsWithRepairs++
			}
			if r.Status == StatusError {
				t.Errors++
			}
			if r.Unsafe != "" {
				t.Unsafe++
			}
			t.InputTokens += r.InputTokens
			t.OutputTokens += r.OutputTokens
			total += r.Duration
		}
	}
	if t.Runs > 0 {
		t.AvgRunDuration = total / time.Duration(t.Runs)
	}
	return t
}

// TerminalSummary is the short text printed when the eval ends.
func TerminalSummary(m Meta, results []CaseResult) string {
	t := Summarize(results)
	var b strings.Builder
	fmt.Fprintf(&b, "Eval: %s (%s) · %d brief × %d çalıştırma · %s\n", m.Model, m.Provider, t.Briefs, m.Runs,
		m.StartedAt.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "Doğruluk:    %d/%d çalıştırma (%s)\n", t.CorrectRuns, t.Runs, pct(t.CorrectRuns, t.Runs))
	fmt.Fprintf(&b, "Tutarlılık:  %d/%d brief (%s)\n", t.ConsistentBriefs, t.Briefs, pct(t.ConsistentBriefs, t.Briefs))
	fmt.Fprintf(&b, "Onarım:      %d çalıştırmada toplam %d onarım · API hatası: %d\n", t.RunsWithRepairs, t.Repairs, t.Errors)
	fmt.Fprintf(&b, "Çağrı/token: %d çağrı · %d input / %d output token · ort. %s/çalıştırma\n",
		m.Calls, t.InputTokens, t.OutputTokens, t.AvgRunDuration.Round(100*time.Millisecond))
	if t.Unsafe > 0 {
		fmt.Fprintf(&b, "GÜVENLİK:    %d çalıştırmada SQL kontrolü başarısız!\n", t.Unsafe)
	}
	return b.String()
}

// Markdown renders the human-readable report.
func Markdown(m Meta, results []CaseResult) string {
	t := Summarize(results)
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Eval raporu: %s\n\n", m.Model)
	w("- **Tarih:** %s (süre %s)\n", m.StartedAt.Local().Format("2006-01-02 15:04"), time.Duration(m.Duration).Round(time.Second))
	w("- **Sağlayıcı:** %s · effort `%s` · commit `%s`\n", m.Provider, m.Effort, m.Commit)
	w("- **Kapsam:** %d brief × %d çalıştırma = %d çalıştırma, %d API çağrısı (onarımlar dahil)\n", t.Briefs, m.Runs, t.Runs, m.Calls)
	w("- **Veri:** sabit seed (RNG %d, %d rastgele kullanıcı + test kohortları), \"bugün\" = %s\n",
		m.RNGSeed, m.SeedUsers, m.SeedNow.Format("2006-01-02"))
	w("- **Puanlama:** Bir çalıştırma, status'ü kabul edilenlerden biriyse, kuralının seçtiği kişiler beklenen kişilerle birebir aynıysa ")
	w("ve beklenen `unsupported` sebepleri yazılmışsa doğrudur. Bir brief'in bütün çalıştırmaları aynı status'ü ve aynı kişileri verdiyse brief tutarlıdır (D-TEST).\n\n")

	w("## Özet\n\n| | Sonuç |\n|---|---|\n")
	w("| Doğruluk | %d/%d çalıştırma (%s) |\n", t.CorrectRuns, t.Runs, pct(t.CorrectRuns, t.Runs))
	w("| Tutarlılık | %d/%d brief (%s) |\n", t.ConsistentBriefs, t.Briefs, pct(t.ConsistentBriefs, t.Briefs))
	w("| Onarım | %d çalıştırmada toplam %d onarım |\n", t.RunsWithRepairs, t.Repairs)
	w("| API hatası | %d |\n", t.Errors)
	w("| Token | %d input / %d output |\n", t.InputTokens, t.OutputTokens)
	w("| Ortalama süre | %s / çalıştırma |\n", t.AvgRunDuration.Round(100*time.Millisecond))
	if t.Unsafe > 0 {
		w("| **Güvenlik** | **%d çalıştırmada SQL kontrolü başarısız** |\n", t.Unsafe)
	} else {
		w("| Güvenlik | Derlenen bütün SQL'lerde izin koşulu var, SQL metninde kuraldan gelen değer yok |\n")
	}

	w("\n## Kategoriye göre\n\n| Kategori | Brief | Doğruluk | Tutarlılık |\n|---|---|---|---|\n")
	var categories []string
	for _, cr := range results {
		if !slices.Contains(categories, cr.Case.Category) {
			categories = append(categories, cr.Case.Category)
		}
	}
	for _, cat := range categories {
		var group []CaseResult
		for _, cr := range results {
			if cr.Case.Category == cat {
				group = append(group, cr)
			}
		}
		ct := Summarize(group)
		w("| %s | %d | %d/%d | %d/%d |\n", cat, ct.Briefs, ct.CorrectRuns, ct.Runs, ct.ConsistentBriefs, ct.Briefs)
	}

	w("\n## Brief'ler\n\n| # | Kategori | Beklenen |")
	for i := range m.Runs {
		w(" Çalıştırma %d |", i+1)
	}
	w(" Doğru | Tutarlı |\n|---|---|---|")
	for range m.Runs {
		w("---|")
	}
	w("---|---|\n")
	for _, cr := range results {
		w("| %d | %s | %s |", cr.Case.ID, cr.Case.Category, expectedCell(cr))
		for i, r := range cr.Runs {
			w(" %s |", runCell(r, cr.Verdicts[i]))
		}
		w(" %d/%d | %s |\n", cr.CorrectRuns(), len(cr.Runs), check(Consistent(cr.Runs)))
	}

	w("\n## Hatalı çalıştırmalar\n\n")
	wrong := 0
	for _, cr := range results {
		for i, v := range cr.Verdicts {
			if v.Correct {
				continue
			}
			wrong++
			r := cr.Runs[i]
			w("### #%d · çalıştırma %d\n\n", cr.Case.ID, i+1)
			w("- **Brief:** %s\n", cr.Case.Brief)
			w("- **Sorun:** %s\n", strings.Join(v.Problems, "; "))
			if len(cr.Case.Rules) > 0 {
				w("- **Beklenen:** %s\n", ruleLine(cr.Case.Rules[0]))
			}
			writeRunDetails(&b, r)
			w("\n")
		}
	}
	if wrong == 0 {
		w("Hatalı çalıştırma yok.\n\n")
	}

	w("## Tutarsız brief'ler\n\nAynı brief'in çalıştırmaları farklı sonuç verdi (doğru olsalar bile).\n\n")
	inconsistent := 0
	for _, cr := range results {
		if Consistent(cr.Runs) {
			continue
		}
		inconsistent++
		w("### #%d: %s\n\n", cr.Case.ID, cr.Case.Brief)
		for i, r := range cr.Runs {
			w("- **Çalıştırma %d** (%s): ", i+1, runCell(r, cr.Verdicts[i]))
			if r.Rule != nil && r.Audience != nil {
				w("%s", ruleLine(*r.Rule))
			} else if r.Error != "" {
				w("%s", r.Error)
			} else {
				w("kitle yok")
			}
			w("\n")
		}
		w("\n")
	}
	if inconsistent == 0 {
		w("Yok.\n\n")
	}

	w("## Varsayımlar\n\nModelin kurala yazdığı varsayımlar (pazarlamacıya onay ekranında gösterilir).\n\n")
	found := false
	for _, cr := range results {
		for i, r := range cr.Runs {
			if r.Rule == nil || len(r.Rule.Assumptions) == 0 {
				continue
			}
			found = true
			w("- **#%d · çalıştırma %d:** %s\n", cr.Case.ID, i+1, strings.Join(r.Rule.Assumptions, " · "))
		}
	}
	if !found {
		w("Yok.\n")
	}
	return b.String()
}

func writeRunDetails(b *strings.Builder, r Run) {
	fmt.Fprintf(b, "- **Model:** status `%s`", r.Status)
	if r.Audience != nil {
		fmt.Fprintf(b, ", %d kişi", len(r.Audience))
	}
	if r.Repairs > 0 {
		fmt.Fprintf(b, ", %d onarım", r.Repairs)
	}
	b.WriteString("\n")
	if r.Error != "" {
		fmt.Fprintf(b, "  - Hata: %s\n", r.Error)
	}
	if r.Rule == nil {
		return
	}
	if r.Audience != nil {
		fmt.Fprintf(b, "  - Kural: %s\n", ruleLine(*r.Rule))
	}
	for _, u := range r.Rule.Unsupported {
		fmt.Fprintf(b, "  - Uygulanmayan: %s (`%s`)\n", u.Text, u.Reason)
	}
	for _, a := range r.Rule.Assumptions {
		fmt.Fprintf(b, "  - Varsayım: %s\n", a)
	}
}

// ruleLine is the rule's Turkish summary (as shown in the UI) on one line.
func ruleLine(r segment.Rule) string {
	s := segment.Summarize(r)
	if s.All {
		return "Tüm kullanıcılar"
	}
	var parts []string
	if len(s.Common) > 0 {
		parts = append(parts, strings.Join(s.Common, " VE "))
	}
	if len(s.Groups) > 0 {
		groups := make([]string, len(s.Groups))
		for i, g := range s.Groups {
			groups[i] = "(" + strings.Join(g, " VE ") + ")"
		}
		parts = append(parts, "["+strings.Join(groups, " VEYA ")+"]")
	}
	return strings.Join(parts, " VE ")
}

func expectedCell(cr CaseResult) string {
	status := strings.Join(cr.Case.Status, " / ")
	switch cr.Case.Audience {
	case AudienceExact:
		sizes := make([]string, len(cr.Expected))
		for i, e := range cr.Expected {
			sizes[i] = fmt.Sprint(len(e))
		}
		return fmt.Sprintf("%s · %s kişi", status, strings.Join(sizes, " veya "))
	case AudienceAny:
		return status + " · kitle varsayıma bağlı"
	default:
		return status + " · kitle yok"
	}
}

func runCell(r Run, v Verdict) string {
	cell := check(v.Correct) + " " + r.Status
	if r.Audience != nil {
		cell += fmt.Sprintf(" · %d", len(r.Audience))
	}
	return cell
}

func check(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

// pct formats a share the Turkish way: "%91,7".
func pct(n, d int) string {
	if d == 0 {
		return "-"
	}
	return "%" + strings.Replace(fmt.Sprintf("%.1f", 100*float64(n)/float64(d)), ".", ",", 1)
}

// JSON renders every run in full, for inspection and for comparing models.
// It holds rules and counts only, no personal data (user ids are left out).
func JSON(m Meta, results []CaseResult) ([]byte, error) {
	type runJSON struct {
		Run          int           `json:"run"`
		Status       string        `json:"status"`
		Correct      bool          `json:"correct"`
		Problems     []string      `json:"problems"`
		Audience     *int          `json:"audience_size"`
		Extra        int           `json:"extra"`
		Missing      int           `json:"missing"`
		Rule         *segment.Rule `json:"rule"`
		Summary      string        `json:"summary,omitempty"`
		Error        string        `json:"error,omitempty"`
		Unsafe       string        `json:"unsafe,omitempty"`
		Repairs      int           `json:"repairs"`
		Calls        int           `json:"calls"`
		InputTokens  int64         `json:"input_tokens"`
		OutputTokens int64         `json:"output_tokens"`
		Duration     Duration      `json:"duration"`
	}
	type caseJSON struct {
		Case
		ExpectedSizes []int     `json:"expected_sizes"`
		Correct       int       `json:"correct_runs"`
		Consistent    bool      `json:"consistent"`
		Runs          []runJSON `json:"runs"`
	}
	out := struct {
		Meta   Meta       `json:"meta"`
		Totals Totals     `json:"totals"`
		Cases  []caseJSON `json:"cases"`
	}{Meta: m, Totals: Summarize(results)}

	for _, cr := range results {
		cj := caseJSON{Case: cr.Case, Correct: cr.CorrectRuns(), Consistent: Consistent(cr.Runs)}
		for _, e := range cr.Expected {
			cj.ExpectedSizes = append(cj.ExpectedSizes, len(e))
		}
		for i, r := range cr.Runs {
			v := cr.Verdicts[i]
			rj := runJSON{Run: i + 1, Status: r.Status, Correct: v.Correct, Problems: v.Problems,
				Extra: v.Extra, Missing: v.Missing, Rule: r.Rule, Error: r.Error, Unsafe: r.Unsafe,
				Repairs: r.Repairs, Calls: r.Calls, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
				Duration: Duration(r.Duration)}
			if r.Audience != nil {
				n := len(r.Audience)
				rj.Audience = &n
				rj.Summary = ruleLine(*r.Rule)
			}
			cj.Runs = append(cj.Runs, rj)
		}
		out.Cases = append(out.Cases, cj)
	}
	return json.MarshalIndent(out, "", "  ")
}
