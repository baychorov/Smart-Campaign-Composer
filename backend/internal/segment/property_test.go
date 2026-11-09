package segment

import (
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Property tests: instead of a few hand-picked rules, generate many random
// ones (fixed seed, so failures are reproducible) and check invariants that
// must hold for every compiled query.

const propertyRuns = 2000

var placeholderRe = regexp.MustCompile(`\$(\d+)`)

func TestCompiledSQLInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	compiled := 0

	for i := range propertyRuns {
		r := randomRule(rng)
		// Free-text fields are never compiled; put SQL in them to prove it.
		r.Notes = []string{"'; DROP TABLE users; --"}
		r.Assumptions = []string{"1=1) OR (TRUE"}

		res := Validate(r)
		if !res.Valid() {
			continue // contradictory or duplicate groups; covered by TestValidate
		}
		compiled++

		for _, ch := range []Channel{ChannelSMS, ChannelEmail} {
			q, err := CompileAudience(r, ch, testNow)
			if err != nil {
				t.Fatalf("run %d: valid rule did not compile: %v\n%+v", i, err, r)
			}
			prefix := "SELECT u.id FROM users u WHERE " + consentColumns[ch] + " AND ("
			checkQuery(t, i, q, prefix, r)
		}

		cq, err := CompileCounts(r, testNow)
		if err != nil {
			t.Fatalf("run %d: counts did not compile: %v", i, err)
		}
		checkQuery(t, i, cq.Query, "", r)
		if !strings.Contains(cq.SQL, " FROM users u WHERE "+anyConsent+" AND (") {
			t.Fatalf("run %d: counts query lacks the consent filter:\n%s", i, cq.SQL)
		}
	}

	if compiled < propertyRuns/2 {
		t.Fatalf("only %d of %d random rules were valid; generator is too weak", compiled, propertyRuns)
	}
	t.Logf("checked %d compiled rules", compiled)
}

func checkQuery(t *testing.T, run int, q Query, prefix string, r Rule) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("run %d: "+format+"\nSQL: %s", append([]any{run}, append(args, q.SQL)...)...)
	}

	// 1. Consent comes first, outside the rule's parentheses (R-AUD-2).
	if prefix != "" && !strings.HasPrefix(q.SQL, prefix) {
		fail("missing consent prefix %q", prefix)
	}
	if prefix != "" && !strings.HasSuffix(q.SQL, ")") {
		fail("rule is not closed by the outer parenthesis")
	}

	// 2. Parentheses are balanced and the rule's outer "(" opened after the
	//    consent filter is closed only at the very end, so no OR inside the
	//    rule can escape and bypass consent.
	depth := 0
	for i, ch := range q.SQL {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth < 0 {
			fail("unbalanced ')' at byte %d", i)
		}
		if prefix != "" && i >= len(prefix) && i < len(q.SQL)-1 && depth == 0 {
			fail("rule parenthesis closes early at byte %d", i)
		}
	}
	if depth != 0 {
		fail("unbalanced parentheses, depth %d", depth)
	}

	// 3. No literal values in the SQL text: no quotes, statement separators,
	//    comments or non-ASCII (city names are non-ASCII Turkish).
	for _, bad := range []string{"'", `"`, ";", "--", "/*"} {
		if strings.Contains(q.SQL, bad) {
			fail("SQL contains %q", bad)
		}
	}
	for _, ch := range q.SQL {
		if ch > 127 {
			fail("SQL contains non-ASCII %q", ch)
		}
	}
	for _, text := range ruleTexts(r) {
		if strings.Contains(q.SQL, text) {
			fail("rule text %q leaked into SQL", text)
		}
	}

	// 4. Placeholders are exactly $1..$len(args), each used at least once.
	used := map[int]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(q.SQL, -1) {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > len(q.Args) {
			fail("placeholder $%d out of range (%d args)", n, len(q.Args))
		}
		used[n] = true
	}
	if len(used) != len(q.Args) {
		fail("%d args but %d distinct placeholders", len(q.Args), len(used))
	}
}

// ruleTexts collects every free-text value in a rule.
func ruleTexts(r Rule) []string {
	texts := append([]string{}, r.Notes...)
	texts = append(texts, r.Assumptions...)
	collect := func(cs []Condition) {
		for _, c := range cs {
			texts = append(texts, c.Values...)
			if c.From != "" {
				texts = append(texts, c.From, c.To) // dates travel as time.Time args
			}
		}
	}
	collect(r.Common)
	for _, g := range r.Groups {
		collect(g.Conditions)
	}
	return texts
}

// Hostile values must be rejected before compilation, wherever they appear.
func TestHostileValuesNeverCompile(t *testing.T) {
	payloads := []string{
		"'; DROP TABLE users; --",
		"İstanbul') OR TRUE --",
		"x' UNION SELECT email FROM users --",
		"$1",
		"İstanbul\x00",
	}
	for _, p := range payloads {
		rules := map[string]Rule{
			"city value": okRule([]Condition{city(OpIn, p)}),
			"field":      okRule([]Condition{{Type: KindAttribute, Field: Field(p), Op: OpIn, Values: []string{"Ankara"}}}),
			"op":         okRule([]Condition{{Type: KindAttribute, Field: FieldCity, Op: Op(p), Values: []string{"Ankara"}}}),
			"event":      okRule([]Condition{event(EventType(p), true, 30)}),
			"type":       okRule([]Condition{{Type: Kind(p)}}),
			"from":       okRule([]Condition{dateEvent(EventPurchase, true, p, "2026-10-01")}),
			"to":         okRule([]Condition{dateEvent(EventPurchase, true, "2026-10-01", "2026-10-01"+p)}),
			"then event": okRule([]Condition{seqEvent(EventAddToCart, 30, EventType(p), false)}),
		}
		for where, r := range rules {
			if _, err := CompileAudience(r, ChannelSMS, testNow); err == nil {
				t.Errorf("payload %q in %s compiled", p, where)
			}
		}
	}
}
