package mockprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Layer 1: the handler is called directly through httptest.NewRecorder, no
// network. The rate-limit clock is a manual one, so no test sleeps.

const (
	phone = "+905551112233"
	email = "ayse.yilmaz@example.test"
)

var t0 = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// clock is a manual clock for the rate-limit window.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// noFaults is a config that delivers everything at once.
func noFaults() Config {
	cfg := DefaultConfig()
	cfg.ErrorRate, cfg.ThrottleRate, cfg.AcceptThenFailRate, cfg.DelayMax = 0, 0, 0, 0
	return cfg
}

type harness struct {
	srv   *Server
	h     http.Handler
	clock *clock
	logs  *bytes.Buffer
}

func newHarness(cfg Config) *harness {
	logs := &bytes.Buffer{}
	c := &clock{now: t0}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := New(cfg, log, c.Now)
	return &harness{srv: srv, h: srv.Handler(), clock: c, logs: logs}
}

type reply struct {
	status     int
	retryAfter string
	body       map[string]any
	raw        string
}

func (h *harness) post(ctx context.Context, path, key, body string) reply {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	r := reply{status: rec.Code, retryAfter: rec.Header().Get("Retry-After"), raw: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &r.body)
	return r
}

func (h *harness) sms(key string) reply {
	return h.post(context.Background(), "/sms", key, `{"to":"`+phone+`","text":"Merhaba, sepetin seni bekliyor."}`)
}

func (h *harness) email(key string) reply {
	return h.post(context.Background(), "/email", key, `{"to":"`+email+`","subject":"Sepetin","body":"Merhaba"}`)
}

func TestWindow(t *testing.T) {
	ms := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }

	t.Run("limit within one second, free again after it", func(t *testing.T) {
		w := newWindow(3)
		for i, want := range []bool{true, true, true, false} {
			if got := w.add(ms(i * 100)); got != want {
				t.Fatalf("request %d: allowed %v, want %v", i, got, want)
			}
		}
		// At 1.35s the window is (0.35s, 1.35s]: all four have left it.
		if !w.add(ms(1350)) {
			t.Fatal("request after the window passed was rejected")
		}
		if w.max() != 4 {
			t.Fatalf("max %d, want 4 (rejected requests count too)", w.max())
		}
	})

	t.Run("no burst across a second boundary", func(t *testing.T) {
		// A fixed per-second counter would allow 3 at 0.9s and 3 more at
		// 1.1s: 6 within 0.2s. The sliding window allows none of the second
		// batch.
		w := newWindow(3)
		for range 3 {
			w.add(ms(900))
		}
		for i := range 3 {
			if w.add(ms(1100)) {
				t.Fatalf("second batch request %d allowed", i)
			}
		}
	})

	t.Run("exactly one second later is outside", func(t *testing.T) {
		w := newWindow(1)
		w.add(ms(0))
		if !w.add(ms(1000)) {
			t.Fatal("window is (now-1s, now]: a request 1s old must have left it")
		}
	})
}

func TestRateLimit(t *testing.T) {
	h := newHarness(noFaults())
	for i := range 100 {
		if r := h.sms(fmt.Sprint("k", i)); r.status != http.StatusAccepted {
			t.Fatalf("request %d: %d %s", i, r.status, r.raw)
		}
	}
	r := h.sms("k100")
	if r.status != http.StatusTooManyRequests || r.retryAfter != "1" || r.body["error"] != "rate_limited" {
		t.Fatalf("101st request: %d, Retry-After %q, %s", r.status, r.retryAfter, r.raw)
	}
	// Email has its own window (D-RATE: one limit per channel).
	if r := h.email("e1"); r.status != http.StatusAccepted {
		t.Fatalf("email blocked by the SMS limit: %d", r.status)
	}
	h.clock.Add(time.Second)
	if r := h.sms("k100"); r.status != http.StatusAccepted {
		t.Fatalf("after one second: %d %s", r.status, r.raw)
	}

	st := h.srv.Stats()[ChannelSMS]
	if st.Requests != 102 || st.Delivered != 101 || st.RateLimited != 1 || st.MaxPerSecond != 101 {
		t.Fatalf("stats %+v", st)
	}
}

func TestIdempotentReplay(t *testing.T) {
	h := newHarness(noFaults())
	first := h.sms("delivery-1")
	again := h.sms("delivery-1")
	if first.status != http.StatusAccepted || again.raw != first.raw {
		t.Fatalf("replay differs:\n%d %s\n%d %s", first.status, first.raw, again.status, again.raw)
	}
	// Formatting does not matter, content does.
	spaced := h.post(context.Background(), "/sms", "delivery-1", `{ "text": "Merhaba, sepetin seni bekliyor.", "to": "`+phone+`" }`)
	if spaced.raw != first.raw {
		t.Fatalf("same body with other spacing: %d %s", spaced.status, spaced.raw)
	}
	changed := h.post(context.Background(), "/sms", "delivery-1", `{"to":"`+phone+`","text":"Başka metin"}`)
	if changed.status != http.StatusUnprocessableEntity || changed.body["error"] != "idempotency_key_reused" {
		t.Fatalf("same key, other body: %d %s", changed.status, changed.raw)
	}
	// Keys are per channel.
	if r := h.email("delivery-1"); r.status != http.StatusAccepted || r.body["message_id"] == first.body["message_id"] {
		t.Fatalf("email with an SMS key: %d %s", r.status, r.raw)
	}

	st := h.srv.Stats()[ChannelSMS]
	if st.Delivered != 1 || st.Replayed != 2 || st.KeyMismatches != 1 {
		t.Fatalf("stats %+v", st)
	}
}

// TestAcceptThenFail: the mock delivers, then answers 500. The client
// cannot tell whether the message went out, so it retries with the same
// key and gets the original success; no second message (D-DEDUP).
func TestAcceptThenFail(t *testing.T) {
	cfg := noFaults()
	cfg.AcceptThenFailRate = 1
	h := newHarness(cfg)

	if r := h.sms("d1"); r.status != http.StatusInternalServerError {
		t.Fatalf("first attempt: %d %s", r.status, r.raw)
	}
	r := h.sms("d1")
	if r.status != http.StatusAccepted || r.body["message_id"] != "sms_00000001" {
		t.Fatalf("retry: %d %s", r.status, r.raw)
	}
	st := h.srv.Stats()[ChannelSMS]
	if st.Delivered != 1 || st.AcceptedFailed != 1 || st.Replayed != 1 {
		t.Fatalf("stats %+v", st)
	}
}

// TestFailureReleasesKey: a plain 500 or an injected 429 delivers nothing,
// so a retry with the same key is processed again until it succeeds.
func TestFailureReleasesKey(t *testing.T) {
	cfg := noFaults()
	cfg.ErrorRate, cfg.ThrottleRate = 0.4, 0.4
	h := newHarness(cfg)

	attempts := 0
	var last reply
	for attempts < 100 {
		attempts++
		last = h.sms("d1")
		if last.status == http.StatusAccepted {
			break
		}
		if last.status == http.StatusTooManyRequests && last.retryAfter != "1" {
			t.Fatalf("injected 429 without Retry-After: %s", last.raw)
		}
	}
	st := h.srv.Stats()[ChannelSMS]
	if last.status != http.StatusAccepted || st.Delivered != 1 || st.Errors+st.Throttled != int64(attempts-1) || st.Replayed != 0 {
		t.Fatalf("after %d attempts: %d, stats %+v", attempts, last.status, st)
	}
	if attempts == 1 {
		t.Fatal("seed 42 should fail at least once at a 80% fault rate; the test proves nothing")
	}
}

// TestConcurrentSameKey: many parallel requests with one key, each waiting
// a random delay. Exactly one message is delivered; every caller gets that
// message's id or 409 (D-DEDUP layer 4).
func TestConcurrentSameKey(t *testing.T) {
	cfg := noFaults()
	cfg.DelayMax = 5 * time.Millisecond
	h := newHarness(cfg)

	const n = 20
	replies := make([]reply, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { replies[i] = h.sms("same") })
	}
	wg.Wait()

	for i, r := range replies {
		switch {
		case r.status == http.StatusAccepted && r.body["message_id"] == "sms_00000001":
		case r.status == http.StatusConflict && r.body["error"] == "request_in_progress":
		default:
			t.Errorf("reply %d: %d %s", i, r.status, r.raw)
		}
	}
	if st := h.srv.Stats()[ChannelSMS]; st.Delivered != 1 || st.Requests != n {
		t.Fatalf("stats %+v", st)
	}
}

func TestBeginStates(t *testing.T) {
	s := newIdempotency()
	a, b := sha256.Sum256([]byte("a")), sha256.Sum256([]byte("b"))
	steps := []struct {
		do   func() beginResult
		want beginResult
	}{
		{func() beginResult { r, _ := s.begin("k", a); return r }, beginNew},
		{func() beginResult { r, _ := s.begin("k", a); return r }, beginInProgress},
		{func() beginResult { r, _ := s.begin("k", b); return r }, beginMismatch},
		{func() beginResult { s.release("k"); r, _ := s.begin("k", a); return r }, beginNew},
		{func() beginResult { s.finish("k", response{status: 202}); r, _ := s.begin("k", a); return r }, beginReplay},
		{func() beginResult { s.release("k"); r, _ := s.begin("k", a); return r }, beginReplay}, // finished keys are kept
	}
	for i, st := range steps {
		if got := st.do(); got != st.want {
			t.Fatalf("step %d: %v, want %v", i, got, st.want)
		}
	}
}

func TestFaultSequence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ErrorRate, cfg.ThrottleRate, cfg.AcceptThenFailRate = 0.1, 0.05, 0.05
	draw := func(c Config, n int) []outcome {
		f := newFaults(c)
		out := make([]outcome, n)
		for i := range out {
			out[i], _ = f.draw()
		}
		return out
	}

	a, b := draw(cfg, 1000), draw(cfg, 1000)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("same seed gave a different sequence")
	}
	other := cfg
	other.Seed++
	if fmt.Sprint(draw(other, 1000)) == fmt.Sprint(a) {
		t.Fatal("another seed gave the same sequence")
	}

	counts := map[outcome]int{}
	for _, o := range draw(cfg, 20000) {
		counts[o]++
	}
	for o, want := range map[outcome]float64{outcomeError: 0.1, outcomeThrottle: 0.05, outcomeAcceptThenFail: 0.05} {
		if got := float64(counts[o]) / 20000; got < want*0.8 || got > want*1.2 {
			t.Errorf("%v rate %.3f, want about %.2f", o, got, want)
		}
	}
	for _, o := range draw(noFaults(), 1000) {
		if o != outcomeDeliver {
			t.Fatalf("zero rates gave %v", o)
		}
	}
}

// TestResetRepeatsRun: after Reset the same requests get the same replies,
// so a test run can be repeated exactly.
func TestResetRepeatsRun(t *testing.T) {
	cfg := noFaults()
	cfg.ErrorRate, cfg.ThrottleRate, cfg.AcceptThenFailRate = 0.2, 0.1, 0.1
	h := newHarness(cfg)
	run := func() string {
		var b strings.Builder
		for i := range 50 {
			r := h.sms(fmt.Sprint("k", i))
			fmt.Fprintf(&b, "%d %v;", r.status, r.body["message_id"])
		}
		return b.String()
	}
	first := run()
	if r := h.post(context.Background(), "/reset", "", ""); r.status != http.StatusOK {
		t.Fatalf("reset: %d", r.status)
	}
	if st := h.srv.Stats()[ChannelSMS]; st.Requests != 0 || st.MaxPerSecond != 0 {
		t.Fatalf("stats after reset %+v", st)
	}
	if second := run(); second != first {
		t.Fatalf("run after reset differs:\n%s\n%s", first, second)
	}
}

// TestDelay uses a synctest bubble: time inside it is fake and jumps ahead
// when every goroutine is waiting, so a 5-second delay takes no real time.
func TestDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := noFaults()
		cfg.DelayMax = 5 * time.Second
		h := newHarness(cfg)
		h.srv.now = time.Now // the bubble's fake clock

		var longest time.Duration
		for i := range 10 {
			start := time.Now()
			if r := h.sms(fmt.Sprint("k", i)); r.status != http.StatusAccepted {
				t.Fatalf("request %d: %d", i, r.status)
			}
			took := time.Since(start)
			if took > cfg.DelayMax {
				t.Fatalf("request %d took %s, above DelayMax", i, took)
			}
			longest = max(longest, took)
		}
		if longest < time.Second {
			t.Fatalf("10 random delays up to 5s all below 1s (%s): delay not applied", longest)
		}
	})
}

// TestClientGoneDuringDelay: the client cancels while the mock waits. No
// message is sent and the key is free for the retry.
func TestClientGoneDuringDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := noFaults()
		cfg.DelayMax = time.Hour
		h := newHarness(cfg)

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		h.post(ctx, "/sms", "d1", `{"to":"`+phone+`","text":"x"}`)
		st := h.srv.Stats()[ChannelSMS]
		if st.Aborted != 1 || st.Delivered != 0 {
			t.Fatalf("stats %+v", st)
		}
		if r := h.post(context.Background(), "/sms", "d1", `{"to":"`+phone+`","text":"x"}`); r.status != http.StatusAccepted {
			t.Fatalf("retry after abort: %d %s", r.status, r.raw)
		}
	})
}

func TestBadRequests(t *testing.T) {
	h := newHarness(noFaults())
	good := `{"to":"` + phone + `","text":"x"}`
	tests := []struct {
		name, path, key, body, code string
	}{
		{"no key", "/sms", "", good, "invalid_idempotency_key"},
		{"key with space", "/sms", "a b", good, "invalid_idempotency_key"},
		{"key too long", "/sms", strings.Repeat("k", maxKeyLen+1), good, "invalid_idempotency_key"},
		{"not JSON", "/sms", "k", `{to`, "invalid_body"},
		{"unknown field", "/sms", "k", `{"to":"x","text":"y","from":"z"}`, "invalid_body"},
		{"missing text", "/sms", "k", `{"to":"x"}`, "invalid_body"},
		{"blank to", "/sms", "k", `{"to":"  ","text":"y"}`, "invalid_body"},
		{"email without subject", "/email", "k", `{"to":"x","body":"y"}`, "invalid_body"},
		{"sms body on email", "/email", "k", good, "invalid_body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := h.post(context.Background(), tt.path, tt.key, tt.body)
			if r.status != http.StatusBadRequest || r.body["error"] != tt.code {
				t.Fatalf("%d %s", r.status, r.raw)
			}
		})
	}
	// Invalid requests are not counted and do not use up the rate limit.
	for ch, st := range h.srv.Stats() {
		if st.Requests != 0 || st.MaxPerSecond != 0 {
			t.Errorf("%s stats %+v", ch, st)
		}
	}
}

// TestNoPersonalData: recipients and texts never reach the logs or the
// stats (CLAUDE.md rule 4).
func TestNoPersonalData(t *testing.T) {
	cfg := noFaults()
	cfg.ErrorRate, cfg.AcceptThenFailRate = 0.3, 0.3
	h := newHarness(cfg)
	for i := range 20 {
		h.sms(fmt.Sprint("s", i))
		h.email(fmt.Sprint("e", i))
	}
	stats := h.post(context.Background(), "/stats", "", "") // POST is not routed
	if stats.status != http.StatusMethodNotAllowed {
		t.Fatalf("POST /stats: %d", stats.status)
	}
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)

	logs := h.logs.String()
	if !strings.Contains(logs, `"outcome"`) {
		t.Fatal("debug log is empty; the check below proves nothing")
	}
	for _, secret := range []string{phone, email, "sepetin", "Sepetin"} {
		if strings.Contains(logs, secret) || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%q leaked into logs or stats", secret)
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	cfg, err := ConfigFromEnv(env(nil))
	if err != nil || cfg != DefaultConfig() {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	cfg, err = ConfigFromEnv(env(map[string]string{
		"MOCK_ERROR_RATE": "0.3", "MOCK_429_RATE": "0", "MOCK_ACCEPT_THEN_FAIL_RATE": "0.1",
		"MOCK_DELAY_MAX": "200ms", "MOCK_RATE_LIMIT": "50", "MOCK_RETRY_AFTER": "2s", "MOCK_SEED": "7",
	}))
	want := Config{ErrorRate: 0.3, ThrottleRate: 0, AcceptThenFailRate: 0.1, DelayMax: 200 * time.Millisecond,
		RateLimit: 50, RetryAfter: 2 * time.Second, Seed: 7}
	if err != nil || cfg != want {
		t.Fatalf("overrides: %+v, %v", cfg, err)
	}

	for _, bad := range []map[string]string{
		{"MOCK_ERROR_RATE": "1.5"},
		{"MOCK_429_RATE": "-0.1"},
		{"MOCK_ERROR_RATE": "abc"},
		{"MOCK_DELAY_MAX": "5"}, // no unit
		{"MOCK_RATE_LIMIT": "0"},
		{"MOCK_SEED": "-1"},
		{"MOCK_RETRY_AFTER": "500ms"},
		{"MOCK_ERROR_RATE": "0.6", "MOCK_429_RATE": "0.6"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
