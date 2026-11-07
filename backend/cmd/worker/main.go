// Command worker sends queued deliveries to the providers. It also serves
// /healthz, /readyz and /metrics on WORKER_ADDR.
package main

import (
	"context"
	"log/slog"
	"net/http"
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
	// The HTTP server failing (e.g. port in use) stops the worker too.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	db, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	reg := platform.NewRegistry()
	reg.MustRegister(delivery.NewQueueCollector(db, log))
	metrics := delivery.NewMetrics(reg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", platform.Healthz)
	mux.HandleFunc("GET /readyz", platform.Readyz(db))
	mux.Handle("GET /metrics", platform.MetricsHandler(reg))
	srvErr := make(chan error, 1)
	go func() {
		srv := &http.Server{Addr: cfg.WorkerAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		if err := platform.ServeHTTP(ctx, srv); err != nil {
			srvErr <- err
			cancel()
		}
	}()

	w := delivery.New(
		delivery.NewPGStore(db),
		delivery.NewHTTPProvider(cfg.ProviderURL, providerTimeout),
		wcfg, log, metrics, nil, nil,
	)
	log.Info("starting worker", "config", cfg, "send_rate", wcfg.Rate,
		"concurrency", wcfg.Concurrency, "lease", wcfg.Lease.String())
	w.Run(ctx)
	select {
	case err := <-srvErr:
		return err
	default:
	}
	log.Info("worker stopped gracefully")
	return nil
}
