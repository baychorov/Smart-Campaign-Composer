// Package pgtest gives integration tests an isolated, migrated database.
// It needs the Postgres from docker-compose (`make db-up`).
package pgtest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

// NewDB creates a fresh database, applies all migrations and returns a pool
// connected to it. The database is dropped when the test finishes.
func NewDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := NewEmptyDB(t)
	if err := platform.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	return pool
}

// NewEmptyDB is like NewDB but skips migrations.
func NewEmptyDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := platform.FromEnv(os.Getenv)
	if err != nil {
		t.Fatalf("pgtest: config: %v", err)
	}
	pool, drop, err := platform.CreateTempDB(ctx, cfg.DatabaseURL, "scc_test")
	if err != nil {
		t.Fatalf("pgtest: %v (is `make db-up` running?)", err)
	}
	t.Cleanup(func() {
		if err := drop(); err != nil {
			t.Errorf("pgtest: %v", err)
		}
	})
	return pool
}
