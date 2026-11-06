// Command seed applies migrations and fills the database with deterministic
// sample data. It does nothing if users already exist, unless -reset is set.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

func main() {
	log := platform.NewLogger()
	if err := run(log); err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	reset := flag.Bool("reset", false, "drop all tables and seed from scratch")
	users := flag.Int("users", seed.DefaultUserCount, "number of random users")
	rngSeed := flag.Uint64("seed", seed.DefaultSeed, "random seed")
	flag.Parse()

	cfg, err := platform.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if *reset {
		if err := platform.MigrateReset(ctx, pool); err != nil {
			return err
		}
		log.Info("dropped all tables")
	}
	if err := platform.Migrate(ctx, pool); err != nil {
		return err
	}

	seeded, err := seed.IsSeeded(ctx, pool)
	if err != nil {
		return err
	}
	if seeded {
		log.Info("database already seeded, skipping (use -reset to reseed)")
		return nil
	}

	ds := seed.Generate(*rngSeed, *users, time.Now())
	if err := seed.Insert(ctx, pool, ds); err != nil {
		return err
	}
	log.Info("seeded database", "users", len(ds.Users), "events", len(ds.Events), "seed", *rngSeed)
	return nil
}
