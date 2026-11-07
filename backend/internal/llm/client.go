// Package llm talks to Claude: it turns a marketing brief into a validated
// segment rule (and, later, message content). Everything above the Client
// interface is provider-independent and tested with Fake; the Anthropic
// adapter is tested against a local fake SSE server (DECISIONS D-LLM-MOCK).
package llm

import (
	"context"
	"errors"
	"fmt"
)

// Client is the only swappable boundary of this package (R-AI-2). Stream
// sends one single-turn request, calls onDelta for every text fragment as it
// arrives (onDelta may be nil), and returns the full text when the stream
// ends.
//
// A nil error only means the stream completed; the caller must still check
// Response.StopReason before using Response.Text (R-AI-1).
type Client interface {
	Stream(ctx context.Context, req Request, onDelta func(text string)) (Response, error)
}

// Effort mirrors output_config.effort. Sent on every call (D-MODEL).
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
)

// Request is a single-turn request: one system prompt and one user message.
// Repairs are also single-turn (D-LLM-RETRY), so there is no history.
type Request struct {
	System    string
	Prompt    string
	Schema    map[string]any // JSON schema for output_config.format
	MaxTokens int64
	Effort    Effort
}

// StopReason is why the model stopped. Only EndTurn means the text is
// complete and may be parsed.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
)

// Usage is the token count of one call, for logs and metrics (R-OBS-2).
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Response is the outcome of one completed stream.
type Response struct {
	Text       string
	StopReason StopReason
	Usage      Usage
	// Refusal is set only when StopReason is StopRefusal.
	Refusal *Refusal
}

// Refusal carries the stop_details of a refusal.
type Refusal struct {
	Category    string
	Explanation string
}

// ErrStreamInterrupted means the connection broke after the response had
// started. The partial text is discarded and the attempt is restarted from
// scratch (D-LLM-RETRY); the SDK does not retry this case itself.
var ErrStreamInterrupted = errors.New("llm stream interrupted")

// APIError is an HTTP error from the provider, after the SDK's own retries
// for 429/5xx are exhausted. It deliberately carries no response body.
type APIError struct {
	StatusCode int
	Type       string // e.g. "invalid_request_error", "rate_limit_error"
	RequestID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llm api error: status %d (%s), request id %s", e.StatusCode, e.Type, e.RequestID)
}
