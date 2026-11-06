//go:build integration

package platform

import (
	"context"
	"os"
	"testing"
	"time"
)

// Layer 2: needs the Postgres from docker-compose (`make db-up`).
func TestOpenDB(t *testing.T) {
	cfg, err := FromEnv(os.Getenv)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("open db (is `make db-up` running?): %v", err)
	}
	defer pool.Close()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("select 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("got %d, want 1", one)
	}
}
