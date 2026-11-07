// Package mockprovider imitates an external SMS and email provider
// (R-MOCK-1, R-MOCK-2, DECISIONS D-MOCK). It injects errors, delays and
// 429s, enforces 100 requests per second per channel over a sliding window,
// honours Idempotency-Key, and reports statistics for tests.
//
// It imports no business package: the worker talks to it only over HTTP,
// as it would to a real provider. State is in memory and lost on restart.
package mockprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Channels and their endpoints.
const (
	ChannelSMS   = "sms"
	ChannelEmail = "email"
)

const (
	maxBodyBytes = 64 << 10
	maxKeyLen    = 128
)

// SMSRequest is the body of POST /sms.
type SMSRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// EmailRequest is the body of POST /email.
type EmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Accepted is the body of a 202 reply.
type Accepted struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"` // always "accepted"
}

// ChannelStats counts what one channel saw. Requests is every request with
// a valid body; the other counters split it by outcome.
type ChannelStats struct {
	Requests       int64 `json:"requests"`
	Delivered      int64 `json:"delivered"`       // distinct messages, one per key
	Replayed       int64 `json:"replayed"`        // repeated key: stored reply, no new message
	RateLimited    int64 `json:"rate_limited"`    // 429 from the sliding window
	Throttled      int64 `json:"throttled"`       // 429 injected at random
	Errors         int64 `json:"errors"`          // 500, nothing delivered
	AcceptedFailed int64 `json:"accepted_failed"` // delivered, but answered 500
	Conflicts      int64 `json:"conflicts"`       // 409: same key already in progress
	KeyMismatches  int64 `json:"key_mismatches"`  // 422: same key, different body
	Aborted        int64 `json:"aborted"`         // client went away during the delay
	MaxPerSecond   int   `json:"max_per_second"`  // most requests in any one-second window
}

// Server is the mock provider. Create it with New.
type Server struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	mu      sync.Mutex // guards the fields below; Reset swaps them
	windows map[string]*window
	idem    *idempotency
	faults  *faults
	stats   map[string]*ChannelStats

	nextID atomic.Int64
}

// New builds a Server. now is the clock of the rate-limit window; nil
// means time.Now.
func New(cfg Config, log *slog.Logger, now func() time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	s := &Server{cfg: cfg, log: log, now: now}
	s.Reset()
	return s
}

// Reset clears all state and restarts the fault sequence from the seed,
// so each test starts from the same point.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windows = map[string]*window{ChannelSMS: newWindow(s.cfg.RateLimit), ChannelEmail: newWindow(s.cfg.RateLimit)}
	s.idem = newIdempotency()
	s.faults = newFaults(s.cfg)
	s.stats = map[string]*ChannelStats{ChannelSMS: {}, ChannelEmail: {}}
	s.nextID.Store(0)
}

// Stats returns a copy of the counters per channel.
func (s *Server) Stats() map[string]ChannelStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]ChannelStats{}
	for ch, st := range s.stats {
		c := *st
		c.MaxPerSecond = s.windows[ch].max()
		out[ch] = c
	}
	return out
}

// Handler returns the mock's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms", s.send(ChannelSMS))
	mux.HandleFunc("POST /email", s.send(ChannelEmail))
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Stats())
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, _ *http.Request) {
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	})
	return mux
}

// state returns the current per-channel state under the lock, so a
// concurrent Reset cannot hand one request parts of two generations.
func (s *Server) state(ch string) (*window, *idempotency, *faults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windows[ch], s.idem, s.faults
}

// count increments one counter of a channel.
func (s *Server) count(ch string, field func(*ChannelStats) *int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*field(s.stats[ch])++
}

// send handles POST /sms and POST /email. Order of checks:
//
//	body and key → rate limit → idempotency → injected fault → delivery
//
// The rate limit comes first: a real provider counts every request,
// replays included.
func (s *Server) send(ch string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if !validKey(key) {
			writeError(w, http.StatusBadRequest, "invalid_idempotency_key",
				fmt.Sprintf("Idempotency-Key header is required: 1-%d printable ASCII characters", maxKeyLen))
			return
		}
		payload, err := readPayload(w, r, ch)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
			return
		}

		win, idem, flt := s.state(ch)
		s.count(ch, func(c *ChannelStats) *int64 { return &c.Requests })
		if !win.add(s.now()) {
			s.count(ch, func(c *ChannelStats) *int64 { return &c.RateLimited })
			s.tooManyRequests(w)
			return
		}

		// Keys are scoped per channel, like separate provider accounts.
		idemKey := ch + ":" + key
		begin, stored := idem.begin(idemKey, sha256.Sum256(payload))
		switch begin {
		case beginReplay:
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Replayed })
			writeRaw(w, stored)
			return
		case beginInProgress:
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Conflicts })
			writeError(w, http.StatusConflict, "request_in_progress", "a request with this Idempotency-Key is being processed")
			return
		case beginMismatch:
			s.count(ch, func(c *ChannelStats) *int64 { return &c.KeyMismatches })
			writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "Idempotency-Key was used with a different body")
			return
		}

		out, delay := flt.draw()
		if !sleep(r.Context(), delay) {
			// The client gave up; a real provider might or might not have
			// sent the message. Here it did not.
			idem.release(idemKey)
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Aborted })
			return
		}
		// The key is logged (a delivery id); recipient and text never are.
		s.log.Debug("mock provider request", "channel", ch, "idempotency_key", key, "outcome", out.String())

		switch out {
		case outcomeError:
			idem.release(idemKey)
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Errors })
			writeError(w, http.StatusInternalServerError, "internal_error", "injected failure")
		case outcomeThrottle:
			idem.release(idemKey)
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Throttled })
			s.tooManyRequests(w)
		default: // deliver, or deliver and then fail
			id := fmt.Sprintf("%s_%08d", ch, s.nextID.Add(1))
			body, _ := json.Marshal(Accepted{MessageID: id, Status: "accepted"})
			resp := response{status: http.StatusAccepted, body: body}
			idem.finish(idemKey, resp)
			s.count(ch, func(c *ChannelStats) *int64 { return &c.Delivered })
			if out == outcomeAcceptThenFail {
				s.count(ch, func(c *ChannelStats) *int64 { return &c.AcceptedFailed })
				writeError(w, http.StatusInternalServerError, "internal_error", "injected failure after accepting")
				return
			}
			writeRaw(w, resp)
		}
	}
}

func (s *Server) tooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(s.cfg.RetryAfter/time.Second)))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
}

// readPayload decodes and checks the body strictly and returns it in a
// canonical form, so the idempotency hash ignores whitespace.
func readPayload(w http.ResponseWriter, r *http.Request, ch string) ([]byte, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var v any
	switch ch {
	case ChannelSMS:
		var m SMSRequest
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("body is not a valid SMS request: %w", err)
		}
		if strings.TrimSpace(m.To) == "" || strings.TrimSpace(m.Text) == "" {
			return nil, fmt.Errorf("to and text are required")
		}
		v = m
	default:
		var m EmailRequest
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("body is not a valid email request: %w", err)
		}
		if strings.TrimSpace(m.To) == "" || strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.Body) == "" {
			return nil, fmt.Errorf("to, subject and body are required")
		}
		v = m
	}
	return json.Marshal(v)
}

func validKey(k string) bool {
	if k == "" || len(k) > maxKeyLen || !utf8.ValidString(k) {
		return false
	}
	for _, r := range k {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// sleep waits d or until ctx ends; it reports whether the full wait passed.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func writeRaw(w http.ResponseWriter, resp response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	writeRaw(w, response{status: status, body: buf.Bytes()})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}
