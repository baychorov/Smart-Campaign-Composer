//go:build integration

// External test package (platform_test) so it can use pgtest, which itself
// imports platform; an in-package test would create an import cycle.
package platform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
)

var appTables = []string{"users", "user_events", "campaigns", "deliveries"}

func TestMigrateUpDownUp(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.NewEmptyDB(t)

	steps := []struct {
		name       string
		run        func(context.Context, *pgxpool.Pool) error
		wantTables bool
	}{
		{"up", platform.Migrate, true},
		{"up again is a no-op", platform.Migrate, true},
		{"reset", platform.MigrateReset, false},
		{"up after reset", platform.Migrate, true},
	}
	for _, s := range steps {
		if err := s.run(ctx, pool); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		for _, table := range appTables {
			if got := tableExists(t, pool, table); got != s.wantTables {
				t.Errorf("%s: table %s exists=%v, want %v", s.name, table, got, s.wantTables)
			}
		}
	}
}

// One delivery per (campaign, user, channel): the DB-level guard of D-DEDUP.
func TestDeliveriesUniquePerCampaignUserChannel(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.NewDB(t)

	mustExec(t, pool, `INSERT INTO users (id, last_name, email, phone, city, age, sms_opt_in, email_opt_in, created_at)
		VALUES (1, 'Test', 'u1@example.test', '+900000000001', 'İstanbul', 30, true, true, now())`)
	mustExec(t, pool, `INSERT INTO campaigns (id, brief) VALUES (1, 'test')`)

	insert := `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, $1)`
	mustExec(t, pool, insert, "sms")
	mustExec(t, pool, insert, "email") // other channel: allowed

	_, err := pool.Exec(ctx, insert, "sms")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" { // unique_violation
		t.Fatalf("duplicate delivery: want unique_violation, got %v", err)
	}
}

func TestCheckConstraintsRejectUnknownValues(t *testing.T) {
	pool := pgtest.NewDB(t)
	mustExec(t, pool, `INSERT INTO users (id, last_name, email, phone, city, age, sms_opt_in, email_opt_in, created_at)
		VALUES (1, 'Test', 'u1@example.test', '+900000000001', 'İstanbul', 30, true, true, now())`)
	mustExec(t, pool, `INSERT INTO campaigns (id, brief) VALUES (1, 'test')`)

	tests := []struct{ name, sql string }{
		{"event type", `INSERT INTO user_events (user_id, event_type, occurred_at) VALUES (1, 'refund', now())`},
		{"age", `INSERT INTO users (last_name, email, phone, city, age, sms_opt_in, email_opt_in, created_at)
			VALUES ('T', 'u2@example.test', '+900000000002', 'Ankara', 12, true, true, now())`},
		{"channel", `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, 'push')`},
		{"campaign status", `UPDATE campaigns SET status = 'deleted' WHERE id = 1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), tt.sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation
				t.Fatalf("want check_violation, got %v", err)
			}
		})
	}
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", "public."+name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
