//go:build integration

package seed

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
)

func TestInsertWritesFullDataset(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.NewDB(t)
	ds := Generate(DefaultSeed, DefaultUserCount, testNow)

	seeded, err := IsSeeded(ctx, pool)
	if err != nil || seeded {
		t.Fatalf("empty db: seeded=%v err=%v", seeded, err)
	}
	if err := Insert(ctx, pool, ds); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if seeded, err = IsSeeded(ctx, pool); err != nil || !seeded {
		t.Fatalf("after insert: seeded=%v err=%v", seeded, err)
	}

	var users, events int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM user_events)").Scan(&users, &events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if users != len(ds.Users) || events != len(ds.Events) {
		t.Errorf("got %d users / %d events, want %d / %d", users, events, len(ds.Users), len(ds.Events))
	}

	// The id sequence must continue after the seeded ids.
	var newID int64
	err = pool.QueryRow(ctx, `INSERT INTO users (last_name, email, phone, city, age, sms_opt_in, email_opt_in, created_at)
		VALUES ('T', 'new@example.test', '+900000000000', 'Ankara', 30, true, true, now()) RETURNING id`).Scan(&newID)
	if err != nil {
		t.Fatalf("insert after seed: %v", err)
	}
	if want := int64(len(ds.Users)) + 1; newID != want {
		t.Errorf("next user id = %d, want %d", newID, want)
	}
}

// Hand-written query for the case brief under D-CART. The segment compiler
// (PLAN step 4) will generate the equivalent; here it proves the cohorts
// are planted so that the expected users are in or out.
const caseAudienceSQL = `
SELECT u.email FROM users u
WHERE u.city = 'İstanbul'
  AND EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id
              AND e.event_type = 'add_to_cart' AND e.occurred_at >= $1::timestamptz - interval '30 days')
  AND NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id
              AND e.event_type = 'purchase' AND e.occurred_at >= $1::timestamptz - interval '30 days')`

func TestCaseCohortsInSeededDB(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.NewDB(t)
	if err := Insert(ctx, pool, Generate(DefaultSeed, DefaultUserCount, testNow)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	all := queryEmails(t, pool, caseAudienceSQL, testNow)
	sms := queryEmails(t, pool, caseAudienceSQL+" AND u.sms_opt_in", testNow)

	tests := []struct {
		cohort        string
		inAudience    bool
		inSMSAudience bool
	}{
		{CohortCaseTarget, true, true},
		{CohortCaseOldPurchase, true, true},
		{CohortCaseWindowInside, true, true},
		{CohortCaseEmailOnly, true, false},
		{CohortCaseNoConsent, true, false},
		{CohortCasePurchaseThenCart, false, false},
		{CohortCaseCartThenPurchase, false, false},
		{CohortCaseWindowOutside, false, false},
		{CohortCaseOtherCity, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.cohort, func(t *testing.T) {
			email := CohortEmail(tt.cohort)
			if got := slices.Contains(all, email); got != tt.inAudience {
				t.Errorf("in case audience = %v, want %v", got, tt.inAudience)
			}
			if got := slices.Contains(sms, email); got != tt.inSMSAudience {
				t.Errorf("in SMS audience = %v, want %v", got, tt.inSMSAudience)
			}
		})
	}

	// Random users must also match, otherwise counts in the UI are trivial.
	if randomHits := len(all) - 5; randomHits < 10 {
		t.Errorf("only %d random users match the case brief; seed is too sparse", randomHits)
	}
}

func queryEmails(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), sql, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return emails
}
