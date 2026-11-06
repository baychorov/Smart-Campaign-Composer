// Command seed fills the database with deterministic sample data.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

func main() {
	log := platform.NewLogger()
	if err := run(log); err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := platform.Load()
	if err != nil {
		return err
	}

	pool, err := platform.OpenDB(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Migrations and seed data are added in PLAN step 3.
	log.Info("seed: database reachable, nothing to seed yet")
	return nil
}
