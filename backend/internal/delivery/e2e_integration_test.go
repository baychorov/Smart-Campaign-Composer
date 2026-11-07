//go:build integration

package delivery

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/mockprovider"
)

// Layer 4: worker + mock provider (over HTTP, faults on) + Postgres.
// The first worker crashes in the worst place, right after a send and
// before recording it; a second worker finishes the queue. The mock's
// counters then show whether anything was delivered twice (R-SEND-3) or
// faster than the limit (R-SEND-2).

var errCrashed = errors.New("worker crashed")

// crashingStore passes calls to the real store until the first "sent"
// result after `after` recorded results. That result is not stored, the
// worker's context is cancelled, and every later call fails: like a
// process killed between the provider's reply and the DB write. Nothing
// the dying worker does (finishing in-flight sends, releasing claimed rows)
// reaches the database.
type crashingStore struct {
	Store
	after  int
	cancel context.CancelFunc

	mu       sync.Mutex
	recorded int
	dead     bool
}

func (s *crashingStore) isDead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead
}

func (s *crashingStore) Claim(ctx context.Context, ch Channel, limit int, now, lease time.Time) ([]Job, error) {
	if s.isDead() {
		return nil, errCrashed
	}
	return s.Store.Claim(ctx, ch, limit, now, lease)
}

func (s *crashingStore) Complete(ctx context.Context, j Job, d Decision, now time.Time) (bool, error) {
	s.mu.Lock()
	if !s.dead && s.recorded >= s.after && d.Kind == KindSent {
		s.dead = true
		s.cancel()
	}
	if s.dead {
		s.mu.Unlock()
		return false, errCrashed
	}
	s.recorded++
	s.mu.Unlock()
	return s.Store.Complete(ctx, j, d, now)
}

func (s *crashingStore) FinishCampaigns(ctx context.Context, now time.Time) (int64, error) {
	if s.isDead() {
		return 0, errCrashed
	}
	return s.Store.FinishCampaigns(ctx, now)
}

func TestEndToEndCrashAndRestart(t *testing.T) {
	q := newQueue(t, 600)
	ctx := t.Context()

	// Consent withdrawn after approval for a few SMS recipients (R-AUD-2).
	const withdrawn = 5
	if _, err := q.pool.Exec(ctx, `UPDATE users SET sms_opt_in = false WHERE id IN (
SELECT user_id FROM deliveries WHERE channel = 'sms' ORDER BY id LIMIT $1)`, withdrawn); err != nil {
		t.Fatal(err)
	}

	mockCfg := mockprovider.Config{
		ErrorRate: 0.05, ThrottleRate: 0.03, AcceptThenFailRate: 0.05,
		DelayMax: 20 * time.Millisecond, RateLimit: 100, RetryAfter: time.Second, Seed: 42,
	}
	mock := mockprovider.New(mockCfg, slog.New(slog.DiscardHandler), nil)
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	provider := NewHTTPProvider(srv.URL, 2*time.Second)

	cfg := DefaultConfig() // 95 msg/s per channel
	cfg.Lease = 2 * time.Second
	cfg.BatchSize = 20
	cfg.PollInterval = 50 * time.Millisecond
	cfg.Retry.Base, cfg.Retry.Cap = 50*time.Millisecond, 500*time.Millisecond
	log := slog.New(slog.DiscardHandler)

	// Run 1: crashes after 150 recorded results.
	ctx1, cancel1 := context.WithCancel(ctx)
	crash := &crashingStore{Store: NewPGStore(q.pool), after: 150, cancel: cancel1}
	New(crash, provider, cfg, log, nil, nil).Run(ctx1)

	sentAfterCrash := q.count(t, `status = 'sent'`)
	stuck := q.count(t, `status = 'sending'`)
	deliveredAfterCrash := mock.Stats()[mockprovider.ChannelSMS].Delivered + mock.Stats()[mockprovider.ChannelEmail].Delivered
	if stuck == 0 || deliveredAfterCrash <= int64(sentAfterCrash) {
		t.Fatalf("crash did not hit mid-send: %d rows sending, mock delivered %d, db sent %d",
			stuck, deliveredAfterCrash, sentAfterCrash)
	}
	t.Logf("after crash: db sent %d, mock delivered %d, rows left sending %d", sentAfterCrash, deliveredAfterCrash, stuck)

	// Run 2: a fresh worker on the real store, until every row is final
	// and the campaign is completed.
	ctx2, cancel2 := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { New(NewPGStore(q.pool), provider, cfg, log, nil, nil).Run(ctx2); close(done) }()
	deadline := time.Now().Add(2 * time.Minute)
	for q.count(t, `status IN ('pending', 'sending')`) > 0 || q.campaignStatus(t) != "completed" {
		if time.Now().After(deadline) {
			cancel2()
			<-done
			t.Fatalf("not finished: %d open rows, campaign %s", q.count(t, `status IN ('pending', 'sending')`), q.campaignStatus(t))
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel2()
	<-done

	if n := q.count(t, `status = 'failed'`); n != 0 {
		t.Errorf("%d rows failed; with these fault rates and 5 attempts that should not happen", n)
	}
	if n := q.count(t, `status = 'skipped'`); n != withdrawn {
		t.Errorf("%d rows skipped, want %d (withdrawn consent)", n, withdrawn)
	}

	stats := mock.Stats()
	replayed := int64(0)
	for _, ch := range Channels {
		st := stats[string(ch)]
		sent := q.count(t, `channel = $1 AND status = 'sent'`, string(ch))
		t.Logf("%s: db sent %d, mock %+v", ch, sent, st)
		// Exactly once: every sent row is one message at the provider, and
		// no message exists without a sent row (R-SEND-3).
		if st.Delivered != int64(sent) {
			t.Errorf("%s: provider delivered %d messages, db has %d sent rows", ch, st.Delivered, sent)
		}
		// At most 100 requests in any one-second window (R-SEND-2).
		if st.MaxPerSecond > 100 {
			t.Errorf("%s: %d requests in one second", ch, st.MaxPerSecond)
		}
		if st.RateLimited != 0 {
			t.Errorf("%s: worker hit the provider's rate limit %d times", ch, st.RateLimited)
		}
		replayed += st.Replayed
	}
	// Resends with the same key happened (after the crash and after
	// accept-then-fail) and were answered from the provider's store.
	if replayed == 0 {
		t.Error("no replayed request: the scenario did not exercise idempotency")
	}
}
