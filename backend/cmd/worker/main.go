// Command worker sends queued deliveries to the providers.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/delivery"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

// providerTimeout bounds one provider request. It is well below the lease,
// so a hung request cannot outlive its claim.
const providerTimeout = 10 * time.Second

func main() {
	log := platform.NewLogger()
	if err := run(log); err != nil {
		log.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := platform.Load()
	if err != nil {
		return err
	}
	wcfg, err := delivery.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	w := delivery.New(
		delivery.NewPGStore(db),
		delivery.NewHTTPProvider(cfg.ProviderURL, providerTimeout),
		wcfg, log, nil, nil,
	)
	log.Info("starting worker", "config", cfg, "send_rate", wcfg.Rate,
		"concurrency", wcfg.Concurrency, "lease", wcfg.Lease.String())
	w.Run(ctx)
	log.Info("worker stopped gracefully")
	return nil
}
