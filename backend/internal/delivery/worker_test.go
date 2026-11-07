package delivery

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Layer 1: the worker against an in-memory Store and a scripted Provider,
// inside a synctest bubble. Time in the bubble is fake and jumps forward
// whenever every goroutine is blocked, so seconds of rate limiting and
// backoff run instantly and exactly.

// fakeStore mimics PGStore's semantics: due rows, lease, attempts as the
// fencing token.
type fakeStore struct {
	mu   sync.Mutex
	rows []*fakeRow
}

type fakeRow struct {
	job         Job
	status      string
	nextAt      time.Time
	lockedUntil time.Time
	lastError   string
}

func newFakeStore() *fakeStore { return &fakeStore{} }

func (s *fakeStore) add(j Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.SMSText == "" {
		j.SMSText = "Merhaba {{first_name}}"
	}
	if j.EmailSubject == "" {
		j.EmailSubject, j.EmailBody = "Konu", "Gövde"
	}
	s.rows = append(s.rows, &fakeRow{job: j, status: "pending"})
}

func (s *fakeStore) Claim(_ context.Context, ch Channel, limit int, now, leaseUntil time.Time) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, r := range s.rows {
		if len(out) == limit {
			break
		}
		due := (r.status == "pending" && !r.nextAt.After(now)) || (r.status == "sending" && !r.lockedUntil.After(now))
		if r.job.Channel != ch || !due {
			continue
		}
		r.status, r.lockedUntil = "sending", leaseUntil
		r.job.Attempts++
		out = append(out, r.job)
	}
	return out, nil
}

func (s *fakeStore) Complete(_ context.Context, j Job, d Decision, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.job.ID != j.ID {
			continue
		}
		if r.status != "sending" || r.job.Attempts != j.Attempts {
			return false, nil
		}
		r.lastError = d.Error
		switch d.Kind {
		case KindSent, KindFailed, KindSkipped:
			r.status = string(d.Kind)
		case KindRetry:
			r.status, r.nextAt = "pending", now.Add(d.Delay)
			if d.Throttled {
				r.job.Attempts--
				r.job.Throttles++
			}
		case KindRelease:
			r.status, r.nextAt = "pending", now
			r.job.Attempts--
		}
		return true, nil
	}
	return false, nil
}

func (s *fakeStore) FinishCampaigns(context.Context, time.Time) (int64, error) { return 0, nil }

func (s *fakeStore) snapshot() map[int64]fakeRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int64]fakeRow{}
	for _, r := range s.rows {
		out[r.job.ID] = *r
	}
	return out
}

func (s *fakeStore) allFinal() bool {
	for _, r := range s.snapshot() {
		if r.status == "pending" || r.status == "sending" {
			return false
		}
	}
	return true
}

// fakeProvider answers with reply and records every call.
type fakeProvider struct {
	mu    sync.Mutex
	calls []call
	reply func(m Message, n int) SendResult // n: calls so far for this key
}

type call struct {
	at  time.Time
	msg Message
}

func (p *fakeProvider) Send(_ context.Context, m Message) SendResult {
	p.mu.Lock()
	p.calls = append(p.calls, call{at: time.Now(), msg: m})
	n := 0
	for _, c := range p.calls {
		if c.msg.IdempotencyKey == m.IdempotencyKey {
			n++
		}
	}
	p.mu.Unlock()
	return p.reply(m, n)
}

func (p *fakeProvider) callsFor(key string) []call {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []call
	for _, c := range p.calls {
		if c.msg.IdempotencyKey == key {
			out = append(out, c)
		}
	}
	return out
}

func accept(Message, int) SendResult { return SendResult{StatusCode: 202} }

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Rate = 100
	cfg.PollInterval = 50 * time.Millisecond
	cfg.Retry.Base = 100 * time.Millisecond
	return cfg
}

// runUntilDone runs the worker until every row is final, then stops it.
func runUntilDone(t *testing.T, w *Worker, s *fakeStore) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	for deadline := time.Now().Add(time.Hour); !s.allFinal(); {
		if time.Now().After(deadline) {
			t.Fatal("rows not final after an hour of fake time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestRateLimit: 250 messages per channel at 100/s. In fake time the sends
// are exactly 10 ms apart: no one-second window holds more than 100, and
// the channels run side by side (2.5 s in total, not 5 s).
func TestRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newFakeStore()
		for i := range 500 {
			ch := ChannelSMS
			if i%2 == 1 {
				ch = ChannelEmail
			}
			s.add(Job{ID: int64(i + 1), Channel: ch, Consent: true, To: "x"})
		}
		p := &fakeProvider{reply: accept}
		start := time.Now()
		runUntilDone(t, New(s, p, testConfig(), discard(), nil, nil), s)

		if took := time.Since(start); took < 2400*time.Millisecond || took > 3*time.Second {
			t.Errorf("500 messages over two channels took %s, want ~2.5 s", took)
		}
		byChannel := map[Channel][]time.Time{}
		for _, c := range p.calls {
			byChannel[c.msg.Channel] = append(byChannel[c.msg.Channel], c.at)
		}
		for ch, times := range byChannel {
			if len(times) != 250 {
				t.Errorf("%s: %d sends", ch, len(times))
			}
			if m := maxPerSecond(times); m > 100 {
				t.Errorf("%s: %d sends in one second", ch, m)
			}
		}
	})
}

// maxPerSecond is the most timestamps in any window (t-1s, t]; times are
// in send order, which is time order.
func maxPerSecond(times []time.Time) int {
	best, lo := 0, 0
	for hi, t := range times {
		for !times[lo].After(t.Add(-time.Second)) {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	return best
}

// TestDecisionsApplied: each provider answer ends in the right state.
func TestDecisionsApplied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const phone = "+905551112233"
		s := newFakeStore()
		for id := range int64(5) {
			s.add(Job{ID: id + 1, Channel: ChannelSMS, Consent: id != 3, To: phone, FirstName: "Ayşe"})
		}
		p := &fakeProvider{reply: func(m Message, n int) SendResult {
			switch m.IdempotencyKey {
			case "1": // fails once, then succeeds
				if n == 1 {
					return SendResult{StatusCode: 500}
				}
			case "2": // throttled once
				if n == 1 {
					return SendResult{StatusCode: 429, RetryAfter: time.Second}
				}
			case "3":
				return SendResult{StatusCode: 400}
			case "5": // always down
				return SendResult{StatusCode: 503}
			}
			return SendResult{StatusCode: 202}
		}}
		logs := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		runUntilDone(t, New(s, p, testConfig(), log, nil, nil), s)

		rows := s.snapshot()
		want := map[int64]struct {
			status              string
			attempts, throttles int
			calls               int
		}{
			1: {"sent", 2, 0, 2},
			2: {"sent", 1, 1, 2}, // the 429 does not use an attempt
			3: {"failed", 1, 0, 1},
			4: {"skipped", 1, 0, 0}, // no consent: never sent
			5: {"failed", 5, 0, 5},
		}
		for id, w := range want {
			r := rows[id]
			calls := len(p.callsFor(strconv.FormatInt(id, 10)))
			if r.status != w.status || r.job.Attempts != w.attempts || r.job.Throttles != w.throttles || calls != w.calls {
				t.Errorf("row %d: status %s attempts %d throttles %d calls %d, want %+v (last error %q)",
					id, r.status, r.job.Attempts, r.job.Throttles, calls, w, r.lastError)
			}
		}
		// The 429 retry waited for Retry-After.
		if c := p.callsFor("2"); c[1].at.Sub(c[0].at) < time.Second {
			t.Errorf("retry after 429 came %s later, Retry-After was 1s", c[1].at.Sub(c[0].at))
		}
		if rows[5].lastError != "http 503, gave up after 5 attempts" {
			t.Errorf("last error %q", rows[5].lastError)
		}
		if bytes.Contains(logs.Bytes(), []byte(phone)) || bytes.Contains(logs.Bytes(), []byte("Ayşe")) {
			t.Error("recipient data in the logs")
		}
	})
}

// TestGracefulShutdown: on cancel, requests already sent are finished and
// recorded; claimed rows not yet sent go back to pending without losing an
// attempt. Nothing is left "sending".
func TestGracefulShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newFakeStore()
		for i := range 100 {
			s.add(Job{ID: int64(i + 1), Channel: ChannelSMS, Consent: true, To: "x"})
		}
		p := &fakeProvider{reply: func(Message, int) SendResult {
			time.Sleep(200 * time.Millisecond) // a slow provider
			return SendResult{StatusCode: 202}
		}}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { New(s, p, testConfig(), discard(), nil, nil).Run(ctx); close(done) }()
		time.Sleep(250 * time.Millisecond)
		cancel()
		<-done

		sent, pending := 0, 0
		for id, r := range s.snapshot() {
			switch r.status {
			case "sent":
				sent++
			case "pending":
				pending++
				if r.job.Attempts != 0 {
					t.Errorf("released row %d has %d attempts", id, r.job.Attempts)
				}
			default:
				t.Errorf("row %d left %s", id, r.status)
			}
		}
		if sent != len(p.calls) || sent == 0 || pending == 0 {
			t.Fatalf("sent %d, provider calls %d, pending %d", sent, len(p.calls), pending)
		}
	})
}

// recordFailStore fails every Complete: the worker sent, then could not
// record. The rows stay "sending" until their lease expires.
type recordFailStore struct{ *fakeStore }

func (recordFailStore) Complete(context.Context, Job, Decision, time.Time) (bool, error) {
	return false, context.DeadlineExceeded
}

// TestUnrecordedSendIsResent: a send whose result could not be stored is
// sent again after the lease, with the same Idempotency-Key, so a provider
// with idempotency delivers it once (D-DEDUP layers 3 and 4).
func TestUnrecordedSendIsResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newFakeStore()
		s.add(Job{ID: 7, Channel: ChannelSMS, Consent: true, To: "x"})
		p := &fakeProvider{reply: accept}
		cfg := testConfig()
		cfg.Lease = 30 * time.Second

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { New(recordFailStore{s}, p, cfg, discard(), nil, nil).Run(ctx); close(done) }()
		time.Sleep(time.Second)
		cancel()
		<-done
		if r := s.snapshot()[7]; r.status != "sending" || len(p.calls) != 1 {
			t.Fatalf("after the failed record: status %s, calls %d", r.status, len(p.calls))
		}

		start := time.Now()
		runUntilDone(t, New(s, p, cfg, discard(), nil, nil), s)
		c := p.callsFor("7")
		if len(c) != 2 || c[1].at.Sub(start) < 29*time.Second {
			t.Fatalf("resend: %d calls, second %s after restart (lease 30s)", len(c), c[len(c)-1].at.Sub(start))
		}
		if s.snapshot()[7].status != "sent" {
			t.Fatal("row not sent after the resend")
		}
	})
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cfg, err := ConfigFromEnv(env(nil))
	if err != nil || cfg != DefaultConfig() {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	cfg, err = ConfigFromEnv(env(map[string]string{"WORKER_SEND_RATE": "50", "WORKER_CONCURRENCY": "2", "WORKER_LEASE": "5s"}))
	if err != nil || cfg.Rate != 50 || cfg.Concurrency != 2 || cfg.Lease != 5*time.Second {
		t.Fatalf("overrides: %+v, %v", cfg, err)
	}
	for _, bad := range []map[string]string{
		{"WORKER_SEND_RATE": "0"}, {"WORKER_SEND_RATE": "x"}, {"WORKER_CONCURRENCY": "0"}, {"WORKER_LEASE": "500ms"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
