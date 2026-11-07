package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
)

// Contract tests (layer 3, D-TEST): the real adapter talks to a local
// httptest server that speaks the Messages API's SSE format. The event
// sequence matches what PLAN step 1 observed on the real API (D-LLM-OUT).

const testKey = "sk-test-not-a-real-key"

type sseEvent struct {
	name string
	data string
}

func ev(name, data string) sseEvent { return sseEvent{name, data} }

func messageStart() sseEvent {
	return ev("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1635,"output_tokens":1}}}`)
}

func textStart(i int) sseEvent {
	return ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i))
}

// textDelta JSON-encodes s, so s is exactly what the model "wrote".
func textDelta(i int, s string) sseEvent {
	b, _ := json.Marshal(s)
	return ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, b))
}

func blockStop(i int) sseEvent {
	return ev("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
}

func messageDelta(stop, details string) sseEvent {
	if details == "" {
		details = "null"
	}
	return ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null,"stop_details":%s},"usage":{"output_tokens":185}}`, stop, details))
}

func messageStop() sseEvent { return ev("message_stop", `{"type":"message_stop"}`) }

// textStream is a complete single-text-block response.
func textStream(stop string, chunks ...string) []sseEvent {
	evs := []sseEvent{messageStart(), textStart(0)}
	for _, c := range chunks {
		evs = append(evs, textDelta(0, c))
	}
	return append(evs, blockStop(0), messageDelta(stop, ""), messageStop())
}

// fakeAPI records requests and replies with a scripted handler per call.
type fakeAPI struct {
	t        *testing.T
	calls    atomic.Int32
	bodies   chan map[string]any
	headers  chan http.Header
	handlers []func(w http.ResponseWriter)
}

func newFakeAPI(t *testing.T, handlers ...func(w http.ResponseWriter)) (*fakeAPI, *httptest.Server) {
	f := &fakeAPI{t: t, bodies: make(chan map[string]any, 10), headers: make(chan http.Header, 10), handlers: handlers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.calls.Add(1))
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		f.bodies <- body
		f.headers <- r.Header.Clone()
		if n > len(f.handlers) {
			t.Errorf("unexpected request %d", n)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.handlers[n-1](w)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func sse(events ...sseEvent) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, e := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
			w.(http.Flusher).Flush()
		}
	}
}

func apiError(status int, typ string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req_test")
		// Retry immediately so SDK retry tests do not sleep.
		w.Header().Set("retry-after-ms", "1")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"test"}}`, typ)
	}
}

func newTestAdapter(url string, retries int) *Anthropic {
	return NewAnthropic(AnthropicConfig{
		APIKey:     platform.Secret(testKey),
		Model:      "claude-sonnet-5-5",
		BaseURL:    url,
		MaxRetries: &retries,
		Timeout:    10 * time.Second,
	})
}

func testRequest() Request {
	return Request{System: "sys", Prompt: "brief", Schema: SegmentSchema(), MaxTokens: 8000, Effort: EffortLow}
}

func TestAnthropicStreamText(t *testing.T) {
	api, srv := newFakeAPI(t, sse(textStream("end_turn", `{"status":`, `"ok"}`)...))
	var deltas []string
	resp, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"status":"ok"}` || resp.StopReason != StopEndTurn {
		t.Errorf("resp = %+v", resp)
	}
	if strings.Join(deltas, "|") != `{"status":|"ok"}` {
		t.Errorf("deltas = %q", deltas)
	}
	if resp.Usage.InputTokens != 1635 || resp.Usage.OutputTokens != 185 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if api.calls.Load() != 1 {
		t.Errorf("calls = %d", api.calls.Load())
	}
}

// sentKey returns the credential the SDK sent. SDK v2.7.0 sends the API key
// as "Authorization: Bearer"; older versions used x-api-key.
func sentKey(h http.Header) string {
	if k := h.Get("X-Api-Key"); k != "" {
		return k
	}
	return strings.TrimPrefix(h.Get("Authorization"), "Bearer ")
}

// TestAnthropicRequestShape pins the request body to D-MODEL and
// D-LLM-OUT: structured output with effort, streaming, no thinking or
// sampling parameters, and the configured key as the credential.
func TestAnthropicRequestShape(t *testing.T) {
	api, srv := newFakeAPI(t, sse(textStream("end_turn", "{}")...))
	if _, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), nil); err != nil {
		t.Fatal(err)
	}
	body := <-api.bodies
	hdr := <-api.headers

	if body["model"] != "claude-sonnet-5-5" || body["max_tokens"] != float64(8000) || body["stream"] != true {
		t.Errorf("model/max_tokens/stream wrong: %v %v %v", body["model"], body["max_tokens"], body["stream"])
	}
	for _, banned := range []string{"thinking", "temperature", "top_p", "top_k", "fallbacks", "tools", "tool_choice"} {
		if _, ok := body[banned]; ok {
			t.Errorf("request must not send %q", banned)
		}
	}
	oc, _ := body["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Errorf("output_config.effort = %v", oc["effort"])
	}
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config.format = %v", format)
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != "sys" {
		t.Errorf("system = %v", body["system"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v (want one user turn, no prefill)", body["messages"])
	}

	if sentKey(hdr) != testKey {
		t.Error("configured API key not sent")
	}
	if hdr.Get("Anthropic-Beta") != "" {
		t.Errorf("no beta header expected, got %q", hdr.Get("Anthropic-Beta"))
	}
}

// TestAnthropicIgnoresEnvironment: the SDK must not pick up a key or base
// URL from the environment; only AnthropicConfig counts.
func TestAnthropicIgnoresEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1") // would fail if used
	api, srv := newFakeAPI(t, sse(textStream("end_turn", "{}")...))
	if _, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), nil); err != nil {
		t.Fatal(err)
	}
	<-api.bodies
	if got := sentKey(<-api.headers); got != testKey {
		t.Errorf("sent key = %q, want the configured key", got)
	}
}

func TestAnthropicSkipsThinkingBlocks(t *testing.T) {
	events := []sseEvent{
		messageStart(),
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"İstanbul + sepet"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`),
		blockStop(0),
		textStart(1), textDelta(1, `{"a":1}`), blockStop(1),
		messageDelta("end_turn", ""), messageStop(),
	}
	_, srv := newFakeAPI(t, sse(events...))
	var deltas []string
	resp, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":1}` || len(deltas) != 1 {
		t.Errorf("text %q, deltas %q: thinking must not leak into the output", resp.Text, deltas)
	}
}

// TestAnthropicSplitEscape: the model's JSON text may contain a \uXXXX
// escape or a \" that the API splits across two deltas (D-LLM-OUT). The
// concatenated text must still decode to the right characters.
func TestAnthropicSplitEscape(t *testing.T) {
	chunks := []string{
		`{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["\u01`,
		`30stanbul"]}],"groups":[],"assumptions":[],"unsupported":[],"notes":["\`,
		`"samimi\" dil"]}`,
	}
	_, srv := newFakeAPI(t, sse(textStream("end_turn", chunks...)...))
	resp, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := decodeRule(resp.Text)
	if err != nil {
		t.Fatal(err)
	}
	if got := rule.Common[0].Values[0]; got != "İstanbul" {
		t.Errorf("city = %q, want İstanbul", got)
	}
	if rule.Notes[0] != `"samimi" dil` {
		t.Errorf("note = %q", rule.Notes[0])
	}
}

func TestAnthropicRefusal(t *testing.T) {
	events := []sseEvent{
		messageStart(), textStart(0), textDelta(0, `{"sta`), blockStop(0),
		messageDelta("refusal", `{"type":"refusal","category":"cyber","explanation":"not allowed"}`),
		messageStop(),
	}
	_, srv := newFakeAPI(t, sse(events...))
	resp, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopRefusal || resp.Refusal == nil || resp.Refusal.Category != "cyber" || resp.Refusal.Explanation != "not allowed" {
		t.Errorf("resp = %+v, refusal = %+v", resp, resp.Refusal)
	}
}

func TestAnthropicMaxTokens(t *testing.T) {
	_, srv := newFakeAPI(t, sse(textStream("max_tokens", `{"status":"ok","aud`)...))
	resp, err := newTestAdapter(srv.URL, 0).Stream(context.Background(), testRequest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopMaxTokens {
		t.Errorf("stop = %q", resp.StopReason)
	}
}

func TestAnthropicStreamInterrupted(t *testing.T) {
	partial := []sseEvent{messageStart(), textStart(0), textDelta(0, `{"status":"o`)}

	t.Run("connection aborted", func(t *testing.T) {
		_, srv := newFakeAPI(t, func(w http.ResponseWriter) {
			sse(partial...)(w)
			panic(http.ErrAbortHandler) // drops the TCP connection mid-body
		})
		_, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		if !errors.Is(err, ErrStreamInterrupted) {
			t.Fatalf("err = %v, want ErrStreamInterrupted", err)
		}
	})

	t.Run("clean close before message_delta", func(t *testing.T) {
		_, srv := newFakeAPI(t, sse(partial...))
		_, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		if !errors.Is(err, ErrStreamInterrupted) {
			t.Fatalf("err = %v, want ErrStreamInterrupted", err)
		}
	})

	t.Run("error event mid-stream", func(t *testing.T) {
		events := append(partial[:len(partial):len(partial)],
			ev("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
		_, srv := newFakeAPI(t, sse(events...))
		_, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		if !errors.Is(err, ErrStreamInterrupted) {
			t.Fatalf("err = %v, want ErrStreamInterrupted", err)
		}
	})
}

func TestAnthropicHTTPErrors(t *testing.T) {
	t.Run("400 is not retried", func(t *testing.T) {
		api, srv := newFakeAPI(t, apiError(400, "invalid_request_error"))
		_, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		var ae *APIError
		if !errors.As(err, &ae) || ae.StatusCode != 400 || ae.Type != "invalid_request_error" || ae.RequestID != "req_test" {
			t.Fatalf("err = %v, want *APIError 400", err)
		}
		if errors.Is(err, ErrStreamInterrupted) {
			t.Error("an error before the stream started is not an interruption")
		}
		if api.calls.Load() != 1 {
			t.Errorf("calls = %d, want 1", api.calls.Load())
		}
	})

	t.Run("401 is not retried", func(t *testing.T) {
		api, srv := newFakeAPI(t, apiError(401, "authentication_error"))
		_, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		var ae *APIError
		if !errors.As(err, &ae) || ae.StatusCode != 401 {
			t.Fatalf("err = %v, want *APIError 401", err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Error("error text must not contain the API key")
		}
		if api.calls.Load() != 1 {
			t.Errorf("calls = %d, want 1", api.calls.Load())
		}
	})

	t.Run("429 then success is retried by the SDK", func(t *testing.T) {
		api, srv := newFakeAPI(t, apiError(429, "rate_limit_error"), sse(textStream("end_turn", "{}")...))
		resp, err := newTestAdapter(srv.URL, 2).Stream(context.Background(), testRequest(), nil)
		if err != nil || resp.Text != "{}" {
			t.Fatalf("resp %+v, err %v", resp, err)
		}
		if api.calls.Load() != 2 {
			t.Errorf("calls = %d, want 2", api.calls.Load())
		}
	})

	t.Run("529 with retries exhausted", func(t *testing.T) {
		api, srv := newFakeAPI(t, apiError(529, "overloaded_error"), apiError(529, "overloaded_error"))
		_, err := newTestAdapter(srv.URL, 1).Stream(context.Background(), testRequest(), nil)
		var ae *APIError
		if !errors.As(err, &ae) || ae.StatusCode != 529 {
			t.Fatalf("err = %v, want *APIError 529", err)
		}
		if api.calls.Load() != 2 {
			t.Errorf("calls = %d, want 2 (1 + 1 retry)", api.calls.Load())
		}
	})
}

// TestAnthropicEndToEnd runs ExtractSegment through the real adapter: a
// broken first answer, then a valid one. It shows the layers fit together
// without a real API key.
func TestAnthropicEndToEnd(t *testing.T) {
	api, srv := newFakeAPI(t,
		sse(textStream("end_turn", `{"status":"ok","audience_scope":"filtered","common":[],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`)...),
		sse(textStream("end_turn", goodIstanbul[:40], goodIstanbul[40:])...),
	)
	res, err := ExtractSegment(context.Background(), newTestAdapter(srv.URL, 0), briefIstanbul)
	if err != nil {
		t.Fatal(err)
	}
	if res.Repairs != 1 || api.calls.Load() != 2 {
		t.Errorf("repairs %d, calls %d", res.Repairs, api.calls.Load())
	}
	if !res.Validation.Approvable() {
		t.Errorf("rule not approvable: %+v", res.Validation)
	}
}
