// Command mockprovider imitates an external SMS/email provider.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/mockprovider"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

func main() {
	log := platform.NewLogger()
	if err := run(log); err != nil {
		log.Error("mockprovider stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := platform.Load()
	if err != nil {
		return err
	}
	mockCfg, err := mockprovider.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", platform.Healthz)
	mux.Handle("/", mockprovider.New(mockCfg, log, nil).Handler())

	log.Info("starting mockprovider", "addr", cfg.MockProviderAddr, "config", mockCfg)
	return platform.ServeHTTP(ctx, &http.Server{
		Addr:              cfg.MockProviderAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	})
}
