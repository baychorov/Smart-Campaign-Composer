//go:build integration

package segment

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// Layer 2. The compiled SQL is checked against a reference evaluator: a
// few lines of plain Go that apply a rule to the in-memory seed dataset.
// If the SQL got parentheses or AND/OR precedence wrong, or normalisation
// changed a rule's meaning, the two user sets differ.

const integrationUsers = 2000

func seededDB(t *testing.T) (*pgxpool.Pool, *reference) {
	t.Helper()
	pool := pgtest.NewDB(t)
	ds := seed.Generate(seed.DefaultSeed, integrationUsers, testNow)
	if err := seed.Insert(t.Context(), pool, ds); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool, newReference(ds)
}

func TestSeedCitiesAreProvinces(t *testing.T) {
	for _, c := range seed.Cities() {
		if got, ok := CanonicalCity(c); !ok || got != c {
			t.Errorf("seed city %q is not a canonical province (got %q, %v)", c, got, ok)
		}
	}
}

// The case brief under both readings. The sequence reading (caseRule)
// differs from the independent one (D-CART) only for users who added to
// the cart after a purchase in the window.
func TestCaseBriefCohorts(t *testing.T) {
	pool, _ := seededDB(t)
	emailOf := emailsByID(t, pool)

	type want struct{ seq, independent bool } // in the SMS audience
	cohorts := map[string]want{
		seed.CohortCaseTarget:           {true, true},
		seed.CohortCaseOldPurchase:      {true, true},
		seed.CohortCaseWindowInside:     {true, true},
		seed.CohortCaseEmailOnly:        {false, false}, // no SMS consent
		seed.CohortCaseNoConsent:        {false, false},
		seed.CohortCasePurchaseThenCart: {true, false},
		seed.CohortCaseCartBuyCart:      {true, false},
		seed.CohortCaseCartThenPurchase: {false, false},
		seed.CohortCaseWindowOutside:    {false, false},
		seed.CohortCaseOtherCity:        {false, false},
	}
	seqSMS := audience(t, pool, caseRule(), ChannelSMS)
	indSMS := audience(t, pool, cartNoPurchaseRule(), ChannelSMS)
	seqEmail := audience(t, pool, caseRule(), ChannelEmail)
	for cohort, w := range cohorts {
		t.Run(cohort, func(t *testing.T) {
			id := emailOf[seed.CohortEmail(cohort)]
			if got := slices.Contains(seqSMS, id); got != w.seq {
				t.Errorf("in sequence audience = %v, want %v", got, w.seq)
			}
			if got := slices.Contains(indSMS, id); got != w.independent {
				t.Errorf("in independent audience = %v, want %v", got, w.independent)
			}
		})
	}
	if id := emailOf[seed.CohortEmail(seed.CohortCaseEmailOnly)]; !slices.Contains(seqEmail, id) {
		t.Error("email-only cohort missing from the email audience")
	}
	// Every independent match is a sequence match: with no purchase in the
	// window, no purchase can follow the cart add.
	for _, id := range indSMS {
		if !slices.Contains(seqSMS, id) {
			t.Fatalf("user %d matches the independent reading but not the sequence", id)
		}
	}
}

func TestPurchaseCountCohorts(t *testing.T) {
	pool, _ := seededDB(t)
	emailOf := emailsByID(t, pool)

	// Brief 11: İzmir, at least 3 purchases in the last 90 days.
	got := audience(t, pool, okRule([]Condition{city(OpIn, "İzmir"), countEvent(EventPurchase, 3, 90)}), ChannelSMS)
	for cohort, want := range map[string]bool{
		seed.CohortBuys2: false, seed.CohortBuys3: true, seed.CohortBuys3Late: false,
	} {
		if in := slices.Contains(got, emailOf[seed.CohortEmail(cohort)]); in != want {
			t.Errorf("%s in audience = %v, want %v", cohort, in, want)
		}
	}
}

func TestAgeBoundaryCohorts(t *testing.T) {
	pool, _ := seededDB(t)
	emailOf := emailsByID(t, pool)

	// Brief 5 without the purchase condition: 25-34 outside İstanbul.
	r := okRule([]Condition{age(OpGte, 25), age(OpLte, 34), city(OpNotIn, "İstanbul")})
	got := audience(t, pool, r, ChannelSMS)
	for cohort, want := range map[string]bool{
		seed.CohortAge24: false, seed.CohortAge25: true, seed.CohortAge34: true, seed.CohortAge35: false,
	} {
		if in := slices.Contains(got, emailOf[seed.CohortEmail(cohort)]); in != want {
			t.Errorf("%s in audience = %v, want %v", cohort, in, want)
		}
	}
}

func TestSQLMatchesReferenceEvaluator(t *testing.T) {
	pool, ref := seededDB(t)
	rng := rand.New(rand.NewPCG(3, 4))

	checked := 0
	for checked < 300 {
		r := randomRule(rng)
		if !Validate(r).Valid() {
			continue
		}
		checked++

		for _, ch := range []Channel{ChannelSMS, ChannelEmail} {
			got := audience(t, pool, r, ch)
			want := ref.audience(r, ch)
			if !slices.Equal(got, want) {
				t.Fatalf("rule %s, %s: SQL returned %d users, reference %d\nnormalised: %s",
					ruleJSON(r), ch, len(got), len(want), ruleJSON(Validate(r).Rule))
			}
		}

		q, err := CompileCounts(r, testNow)
		if err != nil {
			t.Fatalf("compile counts: %v", err)
		}
		counts, err := q.Scan(pool.QueryRow(t.Context(), q.SQL, q.Args...).Scan)
		if err != nil {
			t.Fatalf("counts: %v", err)
		}
		if want := ref.counts(Validate(r).Rule); !countsEqual(counts, want) {
			t.Fatalf("rule %s: counts %+v, reference %+v", ruleJSON(r), counts, want)
		}
	}
}

// ruleJSON prints a rule readably; %+v would show pointer addresses.
func ruleJSON(r Rule) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func countsEqual(a, b Counts) bool {
	return a.Reachable == b.Reachable && a.SMS == b.SMS && a.Email == b.Email && slices.Equal(a.Groups, b.Groups)
}

func audience(t *testing.T, pool *pgxpool.Pool, r Rule, ch Channel) []int64 {
	t.Helper()
	q, err := CompileAudience(r, ch, testNow)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rows, err := pool.Query(t.Context(), q.SQL, q.Args...)
	if err != nil {
		t.Fatalf("query: %v\n%s", err, q.SQL)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	slices.Sort(ids)
	return ids
}

func emailsByID(t *testing.T, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT email, id FROM users WHERE email LIKE 'cohort.%'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	m := map[string]int64{}
	var email string
	var id int64
	if _, err := pgx.ForEachRow(rows, []any{&email, &id}, func() error { m[email] = id; return nil }); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return m
}

// reference evaluates rules in plain Go over the seed dataset.
type reference struct {
	users  []seed.User
	events map[int64]map[string][]time.Time // user -> event type -> times
}

func newReference(ds seed.Dataset) *reference {
	ref := &reference{users: ds.Users, events: map[int64]map[string][]time.Time{}}
	for _, e := range ds.Events {
		if ref.events[e.UserID] == nil {
			ref.events[e.UserID] = map[string][]time.Time{}
		}
		ref.events[e.UserID][e.Type] = append(ref.events[e.UserID][e.Type], e.OccurredAt)
	}
	return ref
}

// audience applies the raw (not normalised) rule, so normalisation bugs
// show up as differences too.
func (ref *reference) audience(r Rule, ch Channel) []int64 {
	var ids []int64
	for _, u := range ref.users {
		consent := u.SMSOptIn
		if ch == ChannelEmail {
			consent = u.EmailOptIn
		}
		if consent && ref.matchesRule(r, u) {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

func (ref *reference) counts(r Rule) Counts {
	var c Counts
	if len(r.Groups) > 0 {
		c.Groups = make([]int64, len(r.Groups))
	}
	for _, u := range ref.users {
		if !(u.SMSOptIn || u.EmailOptIn) || !ref.matchesRule(r, u) {
			continue
		}
		c.Reachable++
		if u.SMSOptIn {
			c.SMS++
		}
		if u.EmailOptIn {
			c.Email++
		}
		for i, g := range r.Groups {
			if ref.matchesAll(g.Conditions, u) {
				c.Groups[i]++
			}
		}
	}
	return c
}

func (ref *reference) matchesRule(r Rule, u seed.User) bool {
	if !ref.matchesAll(r.Common, u) {
		return false
	}
	if len(r.Groups) == 0 {
		return true
	}
	for _, g := range r.Groups {
		if ref.matchesAll(g.Conditions, u) {
			return true
		}
	}
	return false
}

func (ref *reference) matchesAll(cs []Condition, u seed.User) bool {
	for _, c := range cs {
		if !ref.matches(c, u) {
			return false
		}
	}
	return true
}

func (ref *reference) matches(c Condition, u seed.User) bool {
	switch {
	case c.Field == FieldCity:
		in := slices.ContainsFunc(c.Values, func(v string) bool { return foldKey(v) == foldKey(u.City) })
		return in == (c.Op == OpIn)
	case c.Field == FieldAge && c.Op == OpGte:
		return u.Age >= *c.Value
	case c.Field == FieldAge && c.Op == OpLte:
		return u.Age <= *c.Value
	case c.Type == KindEvent:
		// Written independently of compile.go: count the matching events
		// in the window, then look for a Then event after one of them.
		inWindow := func(at time.Time) bool {
			if c.WithinDays != nil {
				return !at.Before(testNow.Add(-time.Duration(*c.WithinDays) * 24 * time.Hour))
			}
			day := at.In(Istanbul).Format(DateLayout) // calendar day in Turkey
			return day >= c.From && day <= c.To
		}
		var hits []time.Time
		for _, at := range ref.events[u.ID][string(c.Event)] {
			if inWindow(at) {
				hits = append(hits, at)
			}
		}
		if !*c.Occurred {
			return len(hits) == 0
		}
		if c.MinCount != nil {
			return len(hits) >= *c.MinCount
		}
		if c.Then == nil {
			return len(hits) > 0
		}
		for _, at := range hits {
			after := slices.ContainsFunc(ref.events[u.ID][string(c.Then.Event)], func(t time.Time) bool { return t.After(at) })
			if after == *c.Then.Occurred {
				return true
			}
		}
		return false
	}
	panic("reference: unknown condition")
}
