// Command worker sends queued deliveries to the providers.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting worker", "config", cfg)
	// The delivery loop is added in PLAN step 9.
	<-ctx.Done()
	log.Info("worker shutting down")
	return nil
}
