// Package pgtest gives integration tests an isolated, migrated database.
// It needs the Postgres from docker-compose (`make db-up`).
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
	admin, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("pgtest: connect (is `make db-up` running?): %v", err)
	}

	name := "scc_test_" + randomHex(8)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		admin.Close()
		t.Fatalf("pgtest: create database: %v", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		admin.Close()
		t.Fatalf("pgtest: invalid DATABASE_URL")
	}
	poolCfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		admin.Close()
		t.Fatalf("pgtest: connect test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("pgtest: drop database %s: %v", name, err)
		}
		admin.Close()
	})
	return pool
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
