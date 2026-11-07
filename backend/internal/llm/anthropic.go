package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

// AnthropicConfig configures the real adapter. Everything comes from
// platform.Config; the SDK does not read the environment itself.
type AnthropicConfig struct {
	APIKey  platform.Secret
	Model   string
	BaseURL string // empty: production API. Tests point it at a fake SSE server.
	// MaxRetries is the SDK's retry count for 429/5xx/connection errors.
	// nil keeps the SDK default (2); make test-live sets 0 (D-TEST).
	MaxRetries *int
	// Timeout bounds one whole Stream call, retries included. Zero means
	// defaultTimeout.
	Timeout time.Duration
}

const defaultTimeout = 2 * time.Minute

// Anthropic is the Client backed by the Claude Messages API (streaming,
// structured output via output_config.format; D-LLM-OUT).
type Anthropic struct {
	client  anthropic.Client
	model   string
	timeout time.Duration
}

// NewAnthropic builds the adapter. It makes no network call.
func NewAnthropic(cfg AnthropicConfig) *Anthropic {
	// WithoutEnvironmentDefaults: the key and base URL come only from cfg,
	// never from env vars or credential files the SDK would otherwise read.
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey.Reveal()),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.MaxRetries != nil {
		opts = append(opts, option.WithMaxRetries(*cfg.MaxRetries))
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: cfg.Model, timeout: timeout}
}

// Stream implements Client. Per D-MODEL it sends effort explicitly and never
// sends thinking or sampling parameters. Thinking blocks, if the model
// produces any, are skipped; only text blocks form Response.Text.
func (a *Anthropic) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: req.MaxTokens,
		System:    []anthropic.TextBlockParam{{Text: req.System}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(req.Effort),
			Format: anthropic.JSONOutputFormatParam{Schema: req.Schema},
		},
	}

	stream := a.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	// Accumulate builds the final message exactly as the API sent it; the
	// text deltas are additionally forwarded as they arrive.
	var msg anthropic.Message
	started := false
	for stream.Next() {
		ev := stream.Current()
		started = true
		if err := msg.Accumulate(ev); err != nil {
			return Response{}, fmt.Errorf("%w: %w", ErrStreamInterrupted, err)
		}
		if onDelta != nil && ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
			onDelta(ev.Delta.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, a.streamError(ctx, err, started)
	}
	// A connection closed cleanly before message_delta leaves no stop
	// reason: the output is incomplete.
	if msg.StopReason == "" {
		return Response{}, fmt.Errorf("%w: stream ended without a stop reason", ErrStreamInterrupted)
	}

	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	resp := Response{
		Text:       text.String(),
		StopReason: StopReason(msg.StopReason),
		Usage:      Usage{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens},
	}
	if resp.StopReason == StopRefusal {
		resp.Refusal = &Refusal{
			Category:    string(msg.StopDetails.Category),
			Explanation: msg.StopDetails.Explanation,
		}
	}
	return resp, nil
}

// streamError classifies a stream failure. Context errors pass through so
// callers can tell cancellation from failure. An API error before any event
// becomes *APIError; anything after the first event means the response was
// cut off and is ErrStreamInterrupted.
func (a *Anthropic) streamError(ctx context.Context, err error, started bool) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("anthropic stream: %w", ctxErr)
	}
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		e := &APIError{StatusCode: apierr.StatusCode, Type: string(apierr.Type()), RequestID: apierr.RequestID}
		if started {
			return fmt.Errorf("%w: %w", ErrStreamInterrupted, e)
		}
		return e
	}
	if started {
		return fmt.Errorf("%w: %w", ErrStreamInterrupted, err)
	}
	return fmt.Errorf("anthropic stream: %w", err)
}
