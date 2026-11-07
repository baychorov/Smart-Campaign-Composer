// Command api serves the HTTP API used by the frontend.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/campaign"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/httpapi"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

func main() {
	log := platform.NewLogger()
	if err := run(log); err != nil {
		log.Error("api stopped", "err", err)
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

	db, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	var client llm.Client
	if cfg.LLMProvider == platform.LLMProviderAnthropic {
		client = llm.NewAnthropic(llm.AnthropicConfig{
			APIKey:  cfg.AnthropicAPIKey,
			Model:   cfg.LLMModel,
			BaseURL: cfg.AnthropicBaseURL,
		})
	} else {
		client = llm.NewDemo()
	}

	reg := platform.NewRegistry()
	api := httpapi.New(httpapi.Config{
		Campaigns:   campaign.NewService(db, client, time.Now),
		Metrics:     reg,
		LLM:         client,
		LLMProvider: cfg.LLMProvider,
		Log:         log,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", platform.Healthz)
	mux.HandleFunc("GET /readyz", platform.Readyz(db))
	mux.Handle("GET /metrics", platform.MetricsHandler(reg))
	api.Register(mux)

	log.Info("starting api", "config", cfg)
	// No WriteTimeout: SSE responses are long-lived (D-STREAM).
	return platform.ServeHTTP(ctx, &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           platform.NewHTTPMetrics(reg).Wrap(mux),
		ReadHeaderTimeout: 10 * time.Second,
	})
}
