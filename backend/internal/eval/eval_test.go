package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

func TestLoadCases(t *testing.T) {
	cases, err := LoadCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 25 {
		t.Fatalf("%d cases, PLAN has 25", len(cases))
	}
	for i, c := range cases {
		if c.ID != i+1 {
			t.Errorf("case %d at position %d", c.ID, i)
		}
		// The ideal reply must pass the production validator with the
		// case's preferred status; otherwise dry-run and tests lie.
		res := segment.Validate(IdealReply(c))
		if !res.Valid() {
			t.Errorf("#%d: ideal reply invalid: %v", c.ID, res.Errors)
		}
		if string(res.Rule.Status) != c.Status[0] {
			t.Errorf("#%d: ideal reply status %s, want %s", c.ID, res.Rule.Status, c.Status[0])
		}
		if res.Approvable() != (c.Audience != AudienceNone) {
			t.Errorf("#%d: ideal reply approvable = %v with audience %q", c.ID, res.Approvable(), c.Audience)
		}
	}
}

func TestCheckCasesRejectsBadExpectations(t *testing.T) {
	age := 5 // below segment.MinAge
	bad := []Case{
		{ID: 1, Category: "x", Brief: "b", Status: []string{"ok"}, Audience: AudienceExact},
		{ID: 1, Category: "x", Brief: "b", Status: []string{"ok"}, Audience: "some"},
		{ID: 1, Category: "x", Brief: "b", Status: []string{"ok"}, Audience: AudienceExact, Rules: []segment.Rule{
			{Common: []segment.Condition{{Type: segment.KindAttribute, Field: segment.FieldAge, Op: segment.OpGte, Value: &age}}},
		}},
	}
	for i, c := range bad {
		if err := checkCases([]Case{c}); err == nil {
			t.Errorf("bad case %d accepted", i)
		}
	}
	if err := checkCases([]Case{{ID: 1, Category: "x", Brief: "b", Status: []string{"ok"}, Audience: AudienceNone},
		{ID: 1, Category: "x", Brief: "c", Status: []string{"ok"}, Audience: AudienceNone}}); err == nil {
		t.Error("duplicate id accepted")
	}
}

func rulePtr(r segment.Rule) *segment.Rule { return &r }

func TestScore(t *testing.T) {
	exact := Case{ID: 1, Status: []string{"ok"}, Audience: AudienceExact}
	alternatives := Case{ID: 11, Status: []string{"partial"}, Audience: AudienceExact, Reasons: []segment.Reason{segment.ReasonAggregate}}
	assumption := Case{ID: 8, Status: []string{"partial"}, Audience: AudienceAny,
		Reasons: []segment.Reason{segment.ReasonUnknownField}, NeedsAssumption: true}
	none := Case{ID: 9, Status: []string{"unsupported", StatusInvalid}, Audience: AudienceNone,
		Reasons: []segment.Reason{segment.ReasonContradiction}}

	okRule := rulePtr(segment.Rule{Status: segment.StatusOK})
	aggregate := rulePtr(segment.Rule{Status: segment.StatusPartial,
		Unsupported: []segment.Unsupported{{Text: "3 kez", Reason: segment.ReasonAggregate}}})
	partialWithAssumption := rulePtr(segment.Rule{Status: segment.StatusPartial, Assumptions: []string{"90 gün"},
		Unsupported: []segment.Unsupported{{Text: "sadık", Reason: segment.ReasonUnknownField}}})
	contradiction := rulePtr(segment.Rule{Status: segment.StatusUnsupported,
		Unsupported: []segment.Unsupported{{Text: "çelişki", Reason: segment.ReasonContradiction}}})

	tests := []struct {
		name        string
		c           Case
		expected    [][]int64
		run         Run
		wantCorrect bool
		wantProblem string
		extra, miss int
	}{
		{"same users", exact, [][]int64{{1, 2, 3}}, Run{Status: "ok", Rule: okRule, Audience: []int64{1, 2, 3}}, true, "", 0, 0},
		{"extra and missing users", exact, [][]int64{{1, 2, 3}}, Run{Status: "ok", Rule: okRule, Audience: []int64{2, 3, 4, 5}},
			false, "kitle farklı: 2 fazla, 1 eksik", 2, 1},
		{"empty audience is not the expected one", exact, [][]int64{{1}}, Run{Status: "ok", Rule: okRule, Audience: []int64{}},
			false, "kitle farklı: 0 fazla, 1 eksik", 0, 1},
		{"wrong status", exact, [][]int64{{1}}, Run{Status: "partial", Rule: okRule, Audience: []int64{1}}, false, "status partial, beklenen ok", 0, 0},
		{"no approvable rule", exact, [][]int64{{1}}, Run{Status: StatusInvalid, Error: "son hatalar: x"}, false, "onaylanabilir bir kural çıkmadı", 0, 0},
		{"second accepted rule", alternatives, [][]int64{{1}, {1, 2}}, Run{Status: "partial", Rule: aggregate, Audience: []int64{1, 2}}, true, "", 0, 0},
		{"reason missing", alternatives, [][]int64{{1}}, Run{Status: "partial", Rule: partialWithAssumption, Audience: []int64{1}},
			false, "aggregate sebebi yok", 0, 0},
		{"any audience with assumption", assumption, nil, Run{Status: "partial", Rule: partialWithAssumption, Audience: []int64{7}}, true, "", 0, 0},
		{"assumption missing", assumption, nil, Run{Status: "partial", Rule: rulePtr(segment.Rule{Status: segment.StatusPartial,
			Unsupported: partialWithAssumption.Unsupported}), Audience: []int64{7}}, false, "varsayım yazılmadı", 0, 0},
		{"no audience as expected", none, nil, Run{Status: "unsupported", Rule: contradiction}, true, "", 0, 0},
		{"invalid accepted, reasons not checked", none, nil, Run{Status: StatusInvalid}, true, "", 0, 0},
		{"unexpected audience", none, nil, Run{Status: "ok", Rule: okRule, Audience: []int64{1, 2}},
			false, "kitle beklenmiyordu ama 2 kişilik", 0, 0},
		{"unsafe SQL is never correct", exact, [][]int64{{1}}, Run{Status: "ok", Rule: okRule, Audience: []int64{1}, Unsafe: "x"},
			false, "güvenlik", 0, 0},
		{"api error", exact, [][]int64{{1}}, Run{Status: StatusError, Error: "529 overloaded"}, false, "(529 overloaded)", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Score(tc.c, tc.expected, tc.run)
			if v.Correct != tc.wantCorrect {
				t.Fatalf("correct = %v, problems %v", v.Correct, v.Problems)
			}
			if tc.wantProblem != "" && !slices.ContainsFunc(v.Problems, func(p string) bool { return strings.Contains(p, tc.wantProblem) }) {
				t.Errorf("problems %v, want one containing %q", v.Problems, tc.wantProblem)
			}
			if v.Extra != tc.extra || v.Missing != tc.miss {
				t.Errorf("extra/missing = %d/%d, want %d/%d", v.Extra, v.Missing, tc.extra, tc.miss)
			}
		})
	}
}

func TestConsistent(t *testing.T) {
	a := Run{Status: "ok", Audience: []int64{1, 2}}
	tests := []struct {
		name string
		runs []Run
		want bool
	}{
		{"same", []Run{a, a, a}, true},
		{"same no audience", []Run{{Status: "rejected"}, {Status: "rejected"}}, true},
		{"different users, same size", []Run{a, {Status: "ok", Audience: []int64{1, 3}}}, false},
		{"different status", []Run{a, {Status: "partial", Audience: []int64{1, 2}}}, false},
		{"api error", []Run{{Status: StatusError}, {Status: StatusError}}, false},
		{"none", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Consistent(tc.runs); got != tc.want {
				t.Errorf("Consistent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckSQL(t *testing.T) {
	// Every query the compiler produces must pass; the bad ones below
	// cannot come from the compiler and stand in for a regression.
	age := 30
	r := segment.Validate(segment.Rule{Status: segment.StatusOK, AudienceScope: segment.ScopeFiltered, Common: []segment.Condition{
		{Type: segment.KindAttribute, Field: segment.FieldCity, Op: segment.OpIn, Values: []string{"İstanbul", "İzmir"}},
		{Type: segment.KindAttribute, Field: segment.FieldAge, Op: segment.OpLte, Value: &age},
	}}).Rule
	for _, ch := range []segment.Channel{segment.ChannelSMS, segment.ChannelEmail} {
		q, err := segment.CompileAudience(r, ch, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckSQL(q, ch); err != nil {
			t.Errorf("%s: compiled SQL rejected: %v", ch, err)
		}
	}

	good := "SELECT u.id FROM users u WHERE u.sms_opt_in AND (u.city = ANY($1))"
	bad := map[string]segment.Query{
		"no consent":   {SQL: "SELECT u.id FROM users u WHERE (u.city = ANY($1))"},
		"wrong prefix": {SQL: "SELECT u.id FROM users u WHERE u.email_opt_in AND (true)"},
		"literal":      {SQL: good + " OR u.city = 'İstanbul'"},
		"separator":    {SQL: good + "; DELETE FROM users"},
		"non-ascii":    {SQL: good + " AND İ"},
	}
	for name, q := range bad {
		if err := CheckSQL(q, segment.ChannelSMS); !errors.Is(err, ErrUnsafeSQL) {
			t.Errorf("%s: err = %v, want ErrUnsafeSQL", name, err)
		}
	}
	if err := CheckSQL(segment.Query{SQL: good}, segment.ChannelSMS); err != nil {
		t.Errorf("good query rejected: %v", err)
	}
}

func TestPct(t *testing.T) {
	for _, tc := range []struct {
		n, d int
		want string
	}{{55, 60, "%91,7"}, {20, 20, "%100,0"}, {0, 0, "-"}} {
		if got := pct(tc.n, tc.d); got != tc.want {
			t.Errorf("pct(%d, %d) = %q, want %q", tc.n, tc.d, got, tc.want)
		}
	}
}

func TestReports(t *testing.T) {
	city := segment.Rule{Status: segment.StatusOK, AudienceScope: segment.ScopeFiltered,
		Common: []segment.Condition{{Type: segment.KindAttribute, Field: segment.FieldCity, Op: segment.OpIn, Values: []string{"İzmir"}}}}
	c := Case{ID: 7, Category: "Karmaşık", Brief: "İzmir'deki kullanıcılar", Status: []string{"ok"}, Audience: AudienceExact,
		Rules: []segment.Rule{city}}
	results := []CaseResult{{
		Case: c, Expected: [][]int64{{10, 11}},
		Runs: []Run{
			{Status: "ok", Rule: &city, Audience: []int64{10, 11}, Calls: 1, InputTokens: 100, OutputTokens: 10, Duration: time.Second},
			{Status: "ok", Rule: &city, Audience: []int64{10, 11, 12}, Calls: 2, Repairs: 1, InputTokens: 200, OutputTokens: 20,
				Duration: 3 * time.Second},
		},
	}}
	for _, r := range results[0].Runs {
		results[0].Verdicts = append(results[0].Verdicts, Score(c, results[0].Expected, r))
	}
	meta := Meta{Model: "m", Provider: "dry-run", Effort: "low", Runs: 2, Calls: 3, Commit: "abc",
		StartedAt: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), SeedNow: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}

	md := Markdown(meta, results)
	for _, want := range []string{
		"| Doğruluk | 1/2 çalıştırma (%50,0) |",
		"| Tutarlılık | 0/1 brief (%0,0) |",
		"| Onarım | 1 çalıştırmada toplam 1 onarım |",
		"| 7 | Karmaşık | ok · 2 kişi | ✅ ok · 2 | ❌ ok · 3 | 1/2 | ❌ |",
		"### #7 · çalıştırma 2",
		"kitle farklı: 1 fazla, 0 eksik",
		"- **Beklenen:** şehri İzmir olan",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lacks %q\n%s", want, md)
		}
	}

	js, err := JSON(meta, results)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Totals Totals `json:"totals"`
		Cases  []struct {
			ExpectedSizes []int `json:"expected_sizes"`
			Runs          []struct {
				Audience *int   `json:"audience_size"`
				Extra    int    `json:"extra"`
				Summary  string `json:"summary"`
			} `json:"runs"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(js, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Totals.CorrectRuns != 1 || decoded.Totals.Repairs != 1 || decoded.Totals.InputTokens != 300 {
		t.Errorf("totals %+v", decoded.Totals)
	}
	runs := decoded.Cases[0].Runs
	if *runs[1].Audience != 3 || runs[1].Extra != 1 || runs[1].Summary == "" || decoded.Cases[0].ExpectedSizes[0] != 2 {
		t.Errorf("runs %+v", runs)
	}
	// Sizes only: user ids never reach the report. Ids would appear as a
	// list of numbers; the only number lists allowed are single sizes.
	var tree any
	if err := json.Unmarshal(js, &tree); err != nil {
		t.Fatal(err)
	}
	if path := numberList(tree, "$"); path != "" {
		t.Errorf("JSON report has a list of numbers at %s (user ids?)", path)
	}
}

// numberList returns the path of the first array of two or more numbers.
func numberList(v any, path string) string {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if p := numberList(x, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		numbers := 0
		for i, x := range v {
			if _, ok := x.(float64); ok {
				numbers++
			}
			if p := numberList(x, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
		if numbers >= 2 {
			return path
		}
	}
	return ""
}
