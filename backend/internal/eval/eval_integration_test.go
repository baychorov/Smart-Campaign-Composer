//go:build integration

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// Layer 2: the whole eval on a seeded Postgres, with scripted model answers
// instead of the API.
func setup(t *testing.T) ([]Case, AudienceFunc) {
	t.Helper()
	cases, err := LoadCases()
	if err != nil {
		t.Fatal(err)
	}
	pool := pgtest.NewDB(t)
	if err := seed.Insert(t.Context(), pool, seed.Generate(seed.DefaultSeed, seed.DefaultUserCount, testNow)); err != nil {
		t.Fatal(err)
	}
	return cases, DBAudience(pool, testNow)
}

func reply(t *testing.T, r segment.Rule) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cond(field segment.Field, op segment.Op, v any) segment.Condition {
	c := segment.Condition{Type: segment.KindAttribute, Field: field, Op: op}
	switch v := v.(type) {
	case int:
		c.Value = &v
	case []string:
		c.Values = v
	}
	return c
}

func ev(e segment.EventType, occurred bool, days int) segment.Condition {
	return segment.Condition{Type: segment.KindEvent, Event: e, Occurred: &occurred, WithinDays: &days}
}

func okRule(common []segment.Condition, groups ...segment.Group) segment.Rule {
	return segment.Rule{Status: segment.StatusOK, AudienceScope: segment.ScopeFiltered, Common: common, Groups: groups}
}

func TestExecuteIdealReplies(t *testing.T) {
	cases, audience := setup(t)
	results, err := Execute(t.Context(), cases, Options{Client: NewIdealClient(cases), Audience: audience, Today: testNow, Runs: 2, Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}
	tot := Summarize(results)
	if tot.CorrectRuns != 50 || tot.ConsistentBriefs != 25 || tot.Unsafe != 0 {
		t.Errorf("ideal replies: %+v", tot)
		for _, cr := range results {
			for _, v := range cr.Verdicts {
				if !v.Correct {
					t.Logf("#%d: %v", cr.Case.ID, v.Problems)
				}
			}
		}
	}
	// The expected audiences must be real, distinct sets; an empty one
	// would make "exact" meaningless.
	for _, cr := range results {
		for i, e := range cr.Expected {
			if len(e) == 0 {
				t.Errorf("#%d: expected rule %d selects nobody", cr.Case.ID, i)
			}
		}
	}
	// The case brief on the fixed seed. Read as "cart, then no purchase"
	// (PLAN step 14b) it is 173 users; the earlier independent reading
	// (D-CART) gave 146. The 27 more bought in the window and then added to
	// the cart again.
	if n := len(results[3].Expected[0]); n != 173 {
		t.Errorf("case brief audience %d, want 173", n)
	}
}

func TestExecuteScoresWrongAnswers(t *testing.T) {
	cases, audience := setup(t)
	istanbul := cond(segment.FieldCity, segment.OpIn, []string{"İstanbul"})
	twoCities := []string{"İstanbul", "İzmir"}

	// Wrong or differently written answers per case, by call number.
	wrong := map[int]func(n int) (segment.Rule, bool){
		// Same audience, written as two groups: correct.
		2: func(int) (segment.Rule, bool) {
			return okRule(nil,
				segment.Group{Conditions: []segment.Condition{cond(segment.FieldCity, segment.OpIn, []string{"Ankara"})}},
				segment.Group{Conditions: []segment.Condition{cond(segment.FieldCity, segment.OpIn, []string{"İzmir"})}}), true
		},
		// "30 yaş altı" read as age ≤ 30: 30-year-olds come in.
		7: func(int) (segment.Rule, bool) {
			return okRule([]segment.Condition{cond(segment.FieldCity, segment.OpIn, twoCities), cond(segment.FieldAge, segment.OpLte, 30)}), true
		},
		// The purchase condition forgotten, only in the second run.
		4: func(n int) (segment.Rule, bool) {
			return okRule([]segment.Condition{istanbul, ev(segment.EventAddToCart, true, 30)}), n == 1
		},
		// The model writes a contradiction; the backend rejects it every
		// time, so no rule comes out after the repairs: accepted for #9.
		9: func(int) (segment.Rule, bool) {
			return okRule([]segment.Condition{ev(segment.EventPurchase, true, 30), ev(segment.EventPurchase, false, 30)}), true
		},
		// Off-topic brief answered with an audience.
		14: func(int) (segment.Rule, bool) { return okRule([]segment.Condition{istanbul}), true },
		// Right status, wrong reason.
		12: func(int) (segment.Rule, bool) {
			return segment.Rule{Status: segment.StatusUnsupported, AudienceScope: segment.ScopeFiltered,
				Unsupported: []segment.Unsupported{{Text: "VIP", Reason: segment.ReasonRanking}}}, true
		},
	}
	client := NewIdealClient(cases)
	client.Override = func(id, n int) (string, bool) {
		if f, ok := wrong[id]; ok {
			if r, ok := f(n); ok {
				return reply(t, r), true
			}
		}
		return "", false
	}

	results, err := Execute(t.Context(), cases, Options{Client: client, Audience: audience, Today: testNow, Runs: 2, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]CaseResult{}
	for _, cr := range results {
		byID[cr.Case.ID] = cr
	}

	tests := []struct {
		id          int
		correct     int
		consistent  bool
		wantProblem string
	}{
		{2, 2, true, ""},
		{7, 0, true, "kitle farklı"},
		{4, 1, false, "kitle farklı"},
		{9, 2, true, ""},
		{14, 0, true, "status ok, beklenen rejected"},
		{12, 0, true, "unknown_field sebebi yok"},
		{1, 2, true, ""},
	}
	for _, tc := range tests {
		cr := byID[tc.id]
		if got := cr.CorrectRuns(); got != tc.correct {
			t.Errorf("#%d: %d correct runs, want %d (problems %v)", tc.id, got, tc.correct, cr.Verdicts)
		}
		if got := Consistent(cr.Runs); got != tc.consistent {
			t.Errorf("#%d: consistent = %v, want %v", tc.id, got, tc.consistent)
		}
		if tc.wantProblem != "" && !slices.ContainsFunc(cr.Verdicts, func(v Verdict) bool {
			return slices.ContainsFunc(v.Problems, func(p string) bool { return strings.Contains(p, tc.wantProblem) })
		}) {
			t.Errorf("#%d: no problem containing %q in %v", tc.id, tc.wantProblem, cr.Verdicts)
		}
	}

	// #7: age ≤ 30 only adds people; #4 without the purchase condition
	// adds the people who bought.
	if v := byID[7].Verdicts[0]; v.Extra == 0 || v.Missing != 0 {
		t.Errorf("#7: extra/missing = %d/%d, want >0/0", v.Extra, v.Missing)
	}
	if v := byID[4].Verdicts[1]; v.Extra == 0 || v.Missing != 0 {
		t.Errorf("#4 run 2: extra/missing = %d/%d, want >0/0", v.Extra, v.Missing)
	}
	// #9 went through the repair loop: 3 calls, no rule.
	if r := byID[9].Runs[0]; r.Status != StatusInvalid || r.Repairs != 2 || r.Calls != 3 {
		t.Errorf("#9: status %s, %d repairs, %d calls", r.Status, r.Repairs, r.Calls)
	}
}

func TestExecuteStopsAtBudget(t *testing.T) {
	cases, audience := setup(t)
	budget := &BudgetClient{Client: NewIdealClient(cases), Max: 5}
	_, err := Execute(t.Context(), cases, Options{Client: budget, Audience: audience, Today: testNow, Runs: 1, Parallel: 2})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
	if budget.Calls() != 5 {
		t.Errorf("%d calls let through, want 5", budget.Calls())
	}
}

func TestExecuteFailsWithoutDatabase(t *testing.T) {
	cases, _ := setup(t)
	broken := func(context.Context, segment.Rule) ([]int64, error) { return nil, errors.New("db down") }
	if _, err := Execute(t.Context(), cases, Options{Client: NewIdealClient(cases), Audience: broken, Today: testNow, Runs: 1, Parallel: 1}); err == nil {
		t.Fatal("a database failure must abort the eval, not count as a wrong answer")
	}
}
