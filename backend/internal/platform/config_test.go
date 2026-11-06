package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// envMap turns a map into a getenv-style lookup so tests never touch the
// real process environment.
func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults when nothing is set",
			env:  map[string]string{},
			want: Config{
				DatabaseURL:      defaultDatabaseURL,
				LLMProvider:      LLMProviderMock,
				LLMModel:         "claude-sonnet-5-5",
				APIAddr:          ":8080",
				MockProviderAddr: ":8090",
			},
		},
		{
			name: "env overrides defaults",
			env: map[string]string{
				"DATABASE_URL":       "postgres://u:p@db:5432/x",
				"LLM_MODEL":          "claude-haiku-5-5",
				"API_ADDR":           ":9000",
				"MOCK_PROVIDER_ADDR": ":9001",
			},
			want: Config{
				DatabaseURL:      "postgres://u:p@db:5432/x",
				LLMProvider:      LLMProviderMock,
				LLMModel:         "claude-haiku-5-5",
				APIAddr:          ":9000",
				MockProviderAddr: ":9001",
			},
		},
		{
			name: "anthropic provider with key",
			env:  map[string]string{"LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test"},
			want: Config{
				DatabaseURL:      defaultDatabaseURL,
				LLMProvider:      LLMProviderAnthropic,
				LLMModel:         "claude-sonnet-5-5",
				AnthropicAPIKey:  "sk-test",
				APIAddr:          ":8080",
				MockProviderAddr: ":8090",
			},
		},
		{
			name:    "anthropic provider without key",
			env:     map[string]string{"LLM_PROVIDER": "anthropic"},
			wantErr: true,
		},
		{
			name:    "unknown provider",
			env:     map[string]string{"LLM_PROVIDER": "openai"},
			wantErr: true,
		},
		{
			name:    "malformed database url",
			env:     map[string]string{"DATABASE_URL": "postgres://u:p@[bad"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromEnv(envMap(tt.env))
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("want ErrInvalidConfig, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// Secrets must not leak through any of the usual output paths (rule 4).
func TestConfigNeverLeaksSecrets(t *testing.T) {
	const apiKey = "sk-ant-should-never-appear"
	const dbPassword = "db-password-should-never-appear"

	cfg, err := FromEnv(envMap(map[string]string{
		"LLM_PROVIDER":      "anthropic",
		"ANTHROPIC_API_KEY": apiKey,
		"DATABASE_URL":      "postgres://scc:" + dbPassword + "@localhost:5432/scc",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var textBuf, jsonBuf bytes.Buffer
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("start", "config", cfg)
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("start", "config", cfg)
	marshalled, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	outputs := map[string]string{
		"slog text":  textBuf.String(),
		"slog json":  jsonBuf.String(),
		"json":       string(marshalled),
		"fmt %v":     fmt.Sprintf("%v", cfg),
		"fmt %+v":    fmt.Sprintf("%+v", cfg),
		"fmt %#v":    fmt.Sprintf("%#v", cfg),
		"secret %s":  fmt.Sprintf("%s", cfg.AnthropicAPIKey),
		"secret str": cfg.AnthropicAPIKey.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, apiKey) {
			t.Errorf("%s leaks API key: %s", name, out)
		}
	}
	for _, name := range []string{"slog text", "slog json"} {
		if strings.Contains(outputs[name], dbPassword) {
			t.Errorf("%s leaks DB password: %s", name, outputs[name])
		}
	}

	if cfg.AnthropicAPIKey.Reveal() != apiKey {
		t.Errorf("Reveal must return the raw key")
	}
}
