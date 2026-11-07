// Package platform holds process-level plumbing shared by all binaries:
// configuration and the database connection.
package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

// LLM provider names accepted in LLM_PROVIDER.
const (
	LLMProviderMock      = "mock"
	LLMProviderAnthropic = "anthropic"
)

// Defaults for non-sensitive settings. Every value can be overridden by an
// environment variable of the same meaning (see .env.example).
const (
	defaultDatabaseURL      = "postgres://scc:scc@localhost:5432/scc?sslmode=disable"
	defaultLLMProvider      = LLMProviderMock
	defaultLLMModel         = "claude-sonnet-5-5"
	defaultAPIAddr          = ":8080"
	defaultMockProviderAddr = ":8090"
	defaultProviderURL      = "http://localhost:8090"
)

// ErrInvalidConfig is wrapped by every validation error returned from FromEnv.
var ErrInvalidConfig = errors.New("invalid config")

// Secret is a string that never reveals its value when printed, logged or
// marshalled. Use Reveal to get the raw value where it is actually needed.
type Secret string

const redacted = "[REDACTED]"

func (s Secret) Reveal() string { return string(s) }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string             { return `"` + s.String() + `"` }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(s.String()) }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

// Config is the typed configuration shared by all binaries.
type Config struct {
	DatabaseURL     string
	LLMProvider     string
	LLMModel        string
	AnthropicAPIKey Secret
	// AnthropicBaseURL is empty for the production API. Set it to point the
	// adapter at a proxy or a local fake server (D-LLM-MOCK).
	AnthropicBaseURL string
	APIAddr          string
	MockProviderAddr string
	// ProviderURL is where the worker sends messages (the mock provider).
	ProviderURL string
}

// Load reads an optional .env file into the process environment (existing
// variables win) and then builds the config from the environment.
func Load() (Config, error) {
	// The binaries may run from the repo root or from backend/.
	for _, path := range []string{".env", "../.env"} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Load(path); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", path, err)
		}
		break
	}
	return FromEnv(os.Getenv)
}

// FromEnv builds and validates the config using getenv as the variable
// source. Tests pass a map lookup instead of os.Getenv.
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		DatabaseURL:      get("DATABASE_URL", defaultDatabaseURL),
		LLMProvider:      get("LLM_PROVIDER", defaultLLMProvider),
		LLMModel:         get("LLM_MODEL", defaultLLMModel),
		AnthropicAPIKey:  Secret(getenv("ANTHROPIC_API_KEY")),
		AnthropicBaseURL: getenv("ANTHROPIC_BASE_URL"),
		APIAddr:          get("API_ADDR", defaultAPIAddr),
		MockProviderAddr: get("MOCK_PROVIDER_ADDR", defaultMockProviderAddr),
		ProviderURL:      get("PROVIDER_URL", defaultProviderURL),
	}

	switch cfg.LLMProvider {
	case LLMProviderMock:
	case LLMProviderAnthropic:
		if cfg.AnthropicAPIKey == "" {
			return Config{}, fmt.Errorf("%w: ANTHROPIC_API_KEY is required when LLM_PROVIDER=%s", ErrInvalidConfig, LLMProviderAnthropic)
		}
	default:
		return Config{}, fmt.Errorf("%w: LLM_PROVIDER must be %q or %q, got %q", ErrInvalidConfig, LLMProviderMock, LLMProviderAnthropic, cfg.LLMProvider)
	}

	if u, err := url.Parse(cfg.ProviderURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("%w: PROVIDER_URL must be an http(s) URL, got %q", ErrInvalidConfig, cfg.ProviderURL)
	}

	if _, err := url.Parse(cfg.DatabaseURL); err != nil {
		// url.Error would echo the URL (and its password), so drop it.
		return Config{}, fmt.Errorf("%w: DATABASE_URL is not a valid URL", ErrInvalidConfig)
	}

	return cfg, nil
}

// LogValue makes slog print the config with secrets hidden.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", redactURL(c.DatabaseURL)),
		slog.String("llm_provider", c.LLMProvider),
		slog.String("llm_model", c.LLMModel),
		slog.Bool("anthropic_api_key_set", c.AnthropicAPIKey != ""),
		slog.String("anthropic_base_url", c.AnthropicBaseURL),
		slog.String("api_addr", c.APIAddr),
		slog.String("mock_provider_addr", c.MockProviderAddr),
		slog.String("provider_url", c.ProviderURL),
	)
}

// redactURL replaces the password in a URL with "xxxxx".
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return redacted
	}
	return u.Redacted()
}
