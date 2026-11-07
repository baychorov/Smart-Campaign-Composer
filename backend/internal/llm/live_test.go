//go:build live

package llm

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Layer 5 (D-TEST): real API, paid. Run only via `make test-live`, which
// the user approves. Exactly two calls: SDK retries are off, and neither
// test goes through the retry/repair logic of generate or ExtractSegment.
// The point is the adapter contract (SDK, API and model still behave as the
// fake SSE server assumes), not model quality; that is `make eval`.

// liveAdapter builds the adapter from the normal config. The key is loaded
// from .env by platform.Load and is never printed.
func liveAdapter(t *testing.T) (*Anthropic, string) {
	t.Helper()
	t.Chdir("../../..") // repo root, where .env lives
	cfg, err := platform.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AnthropicAPIKey == "" {
		t.Fatal("ANTHROPIC_API_KEY is not set (in the environment or .env); make test-live needs a real key")
	}
	retries := 0
	return NewAnthropic(AnthropicConfig{
		APIKey:     cfg.AnthropicAPIKey,
		Model:      cfg.LLMModel,
		BaseURL:    cfg.AnthropicBaseURL,
		MaxRetries: &retries,
		Timeout:    2 * time.Minute,
	}), cfg.LLMModel
}

// TestLiveSegmentEndTurn: call 1 of 2. The case brief with the real schema
// must end with end_turn and pass strict decoding and validation.
func TestLiveSegmentEndTurn(t *testing.T) {
	a, model := liveAdapter(t)
	req := Request{
		System:    segmentSystemPrompt,
		Prompt:    segmentPrompt(caseBrief),
		Schema:    SegmentSchema(),
		MaxTokens: segmentMaxTokens,
		Effort:    EffortLow,
	}

	start := time.Now()
	var first time.Duration
	deltas := 0
	resp, err := a.Stream(context.Background(), req, func(string) {
		if deltas == 0 {
			first = time.Since(start)
		}
		deltas++
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	t.Logf("model %s: stop %s, %d deltas, first delta %v, total %v, tokens in %d / out %d",
		model, resp.StopReason, deltas, first.Round(time.Millisecond), time.Since(start).Round(time.Millisecond),
		resp.Usage.InputTokens, resp.Usage.OutputTokens)

	if resp.StopReason != StopEndTurn {
		t.Fatalf("stop reason %q, want end_turn", resp.StopReason)
	}
	rule, err := decodeRule(resp.Text)
	if err != nil {
		t.Fatalf("strict decode failed: %v\n%s", err, resp.Text)
	}
	v := segment.Validate(rule)
	if !v.Valid() {
		t.Fatalf("validation errors: %v\n%s", v.Errors, resp.Text)
	}
	t.Logf("rule: %s", resp.Text)
	t.Logf("warnings: %v", v.Warnings)

	// Informational only: interpretation quality is measured by make eval.
	want := segment.Validate(caseRuleForLive()).Rule
	if jsonString(v.Rule.Common) == jsonString(want.Common) && len(v.Rule.Groups) == 0 {
		t.Log("audience matches D-CART")
	} else {
		t.Logf("NOTE: audience differs from D-CART; check with make eval")
	}
}

// TestLiveMaxTokens: call 2 of 2. A tiny limit must stop with max_tokens;
// the adapter returns it as a stop reason and nothing is parsed.
func TestLiveMaxTokens(t *testing.T) {
	a, _ := liveAdapter(t)
	req := Request{
		System:    segmentSystemPrompt,
		Prompt:    segmentPrompt(caseBrief),
		Schema:    SegmentSchema(),
		MaxTokens: 40,
		Effort:    EffortLow,
	}
	resp, err := a.Stream(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	t.Logf("stop %s, tokens in %d / out %d, partial text valid JSON: %v",
		resp.StopReason, resp.Usage.InputTokens, resp.Usage.OutputTokens, json.Valid([]byte(resp.Text)))
	if resp.StopReason != StopMaxTokens {
		t.Fatalf("stop reason %q, want max_tokens", resp.StopReason)
	}
}

func caseRuleForLive() segment.Rule {
	return ok(conds(
		city(segment.OpIn, "İstanbul"),
		event(segment.EventAddToCart, true, 30),
		event(segment.EventPurchase, false, 30),
	))
}
