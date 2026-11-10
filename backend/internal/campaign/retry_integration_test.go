//go:build integration

package campaign

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/delivery"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/mockprovider"
)

// Layer 2 and 4: manual retry of failed messages (PLAN step 15e).

// completedWith approves a campaign and finishes it by hand: the first
// `failed` rows fail with spent budgets, the next `skipped` rows are
// skipped, the rest are sent. It returns the campaign and the failed ids.
func completedWith(t *testing.T, pool *pgxpool.Pool, failed, skipped int) (*Service, Campaign, []int64) {
	t.Helper()
	svc := NewService(pool, llm.NewFake(), func() time.Time { return testNow })
	c, _ := approved(t, svc, istanbulRule)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
WITH r AS (SELECT id, row_number() OVER (ORDER BY id) AS n FROM deliveries WHERE campaign_id = $1)
UPDATE deliveries d SET
    status = CASE WHEN r.n <= $2 THEN 'failed' WHEN r.n <= $2 + $3 THEN 'skipped' ELSE 'sent' END,
    attempts = CASE WHEN r.n <= $2 THEN 5 ELSE 1 END,
    throttles = CASE WHEN r.n <= $2 THEN 3 ELSE 0 END,
    last_error = CASE WHEN r.n <= $2 THEN 'http 500, gave up after 5 attempts' END,
    sent_at = CASE WHEN r.n > $2 + $3 THEN $4::timestamptz END
FROM r WHERE d.id = r.id`, c.ID, failed, skipped, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'completed' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	rows, err := pool.Query(ctx, `SELECT id FROM deliveries WHERE campaign_id = $1 AND status = 'failed' ORDER BY id`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != failed {
		t.Fatalf("%d failed rows, want %d", len(ids), failed)
	}
	return svc, c, ids
}

func countRows(t *testing.T, pool *pgxpool.Pool, id int64, where string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM deliveries WHERE campaign_id = $1 AND `+where, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRetryFailed(t *testing.T) {
	pool := seededPool(t)
	svc, c, failedIDs := completedWith(t, pool, 7, 3)
	total := countRows(t, pool, c.ID, `true`)

	got, n, err := svc.RetryFailed(t.Context(), c.ID)
	if err != nil || n != 7 || got.Status != StatusSending {
		t.Fatalf("retry: %v, %d rows, status %s", err, n, got.Status)
	}
	// The same rows (same ids, so the same Idempotency-Key) are pending
	// again with fresh budgets; no row was added.
	var pending []int64
	rows, err := pool.Query(t.Context(), `SELECT id FROM deliveries WHERE campaign_id = $1
AND status = 'pending' AND attempts = 0 AND throttles = 0 AND last_error IS NULL
AND locked_until IS NULL AND next_attempt_at = $2 ORDER BY id`, c.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		pending = append(pending, id)
	}
	if len(pending) != len(failedIDs) {
		t.Fatalf("pending rows %v, want the failed rows %v", pending, failedIDs)
	}
	for i := range pending {
		if pending[i] != failedIDs[i] {
			t.Fatalf("pending rows %v, want the failed rows %v", pending, failedIDs)
		}
	}
	if got := countRows(t, pool, c.ID, `true`); got != total {
		t.Errorf("%d rows after retry, %d before", got, total)
	}
	// Sent and skipped rows are untouched.
	if n := countRows(t, pool, c.ID, `status = 'skipped'`); n != 3 {
		t.Errorf("%d skipped rows, want 3", n)
	}
	if n := countRows(t, pool, c.ID, `status = 'sent' AND sent_at IS NOT NULL`); n != total-10 {
		t.Errorf("%d sent rows, want %d", n, total-10)
	}

	// The worker takes exactly the requeued rows.
	if claimed := claimAll(t, pool, testNow); claimed != 7 {
		t.Fatalf("claimed %d rows, want 7", claimed)
	}
	// While sending, a second retry is refused.
	if _, _, err := svc.RetryFailed(t.Context(), c.ID); !errors.Is(err, ErrNotCompleted) {
		t.Errorf("retry while sending: %v", err)
	}
}

func TestRetryFailedRefusals(t *testing.T) {
	pool := seededPool(t)
	svc, c, _ := completedWith(t, pool, 0, 2)

	// Completed with nothing failed: refused, and the campaign stays completed.
	if _, _, err := svc.RetryFailed(t.Context(), c.ID); !errors.Is(err, ErrNothingToRetry) {
		t.Errorf("retry without failed rows: %v", err)
	}
	if got := statusOf(t, pool, c.ID); got != StatusCompleted {
		t.Errorf("status %s after a refused retry, want completed", got)
	}

	// Not completed yet: approved and draft campaigns.
	a, _ := approved(t, svc, caseRule)
	if _, _, err := svc.RetryFailed(t.Context(), a.ID); !errors.Is(err, ErrNotCompleted) {
		t.Errorf("retry an approved campaign: %v", err)
	}
	if got := statusOf(t, pool, a.ID); got != StatusApproved {
		t.Errorf("status %s, want approved", got)
	}
	_, d := draft(t, pool, caseRule, goodMessage)
	if _, _, err := svc.RetryFailed(t.Context(), d.ID); !errors.Is(err, ErrNotCompleted) {
		t.Errorf("retry a draft: %v", err)
	}
	if _, _, err := svc.RetryFailed(t.Context(), 999_999); !errors.Is(err, ErrNotFound) {
		t.Errorf("retry a missing campaign: %v", err)
	}
}

// TestRetryFailedConcurrent: a double click, many times over, on a pool
// smaller than the number of callers (see TestApproveConcurrent). Exactly
// one retry requeues the rows.
func TestRetryFailedConcurrent(t *testing.T) {
	pool := seededPool(t)
	_, c, _ := completedWith(t, pool, 5, 0)
	svc := NewService(smallPool(t, pool, 2), llm.NewFake(), func() time.Time { return testNow })

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	requeued := make([]int64, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, requeued[i], errs[i] = svc.RetryFailed(t.Context(), c.ID)
		})
	}
	close(start)
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
			if requeued[i] != 5 {
				t.Errorf("retry %d requeued %d rows, want 5", i, requeued[i])
			}
		case errors.Is(err, ErrNotCompleted):
		default:
			t.Errorf("retry %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d retries succeeded, want exactly 1", wins)
	}
	if got := countRows(t, pool, c.ID, `status = 'pending'`); got != 5 {
		t.Fatalf("%d pending rows, want 5", got)
	}
}

// TestClaimReopensCompletedCampaign: a FinishCampaigns statement that read
// the rows before the retry committed can set the campaign back to
// completed while its rows are pending. Claiming them repairs the status,
// and the campaign completes again once they are final.
func TestClaimReopensCompletedCampaign(t *testing.T) {
	pool := seededPool(t)
	svc, c, _ := completedWith(t, pool, 4, 0)
	if _, _, err := svc.RetryFailed(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	// The stale finish.
	if _, err := pool.Exec(t.Context(), `UPDATE campaigns SET status = 'completed' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}

	store := delivery.NewPGStore(pool)
	var jobs []delivery.Job
	for _, ch := range []delivery.Channel{delivery.ChannelSMS, delivery.ChannelEmail} {
		js, err := store.Claim(t.Context(), ch, 100, testNow, testNow.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, js...)
	}
	if len(jobs) != 4 {
		t.Fatalf("claimed %d rows, want 4", len(jobs))
	}
	if got := statusOf(t, pool, c.ID); got != StatusSending {
		t.Fatalf("status %s after claiming requeued rows, want sending", got)
	}
	for _, j := range jobs {
		if ok, err := store.Complete(t.Context(), j, delivery.Decision{Kind: delivery.KindSent}, testNow); err != nil || !ok {
			t.Fatalf("complete %d: %v %v", j.ID, ok, err)
		}
	}
	if _, err := store.FinishCampaigns(t.Context(), testNow); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, pool, c.ID); got != StatusCompleted {
		t.Fatalf("status %s, want completed", got)
	}
}

// TestRetryFailedEndToEnd (layer 4): worker + mock provider over HTTP +
// Postgres. Run 1 gives each row a single attempt against a provider that
// fails often, so rows end failed; some of them were in fact delivered
// (accepted, then 500). The manual retry and run 2 must deliver every
// failed message, and none of them twice (R-SEND-3): the requeued row
// keeps its id, so the provider answers an already delivered one from its
// idempotency store.
func TestRetryFailedEndToEnd(t *testing.T) {
	pool := seededPool(t)
	svc := NewService(pool, llm.NewFake(), func() time.Time { return testNow })
	c, snap := approved(t, svc, istanbulRule)
	total := int(snap.SMS + snap.Email)

	mock := mockprovider.New(mockprovider.Config{
		ErrorRate: 0.3, AcceptThenFailRate: 0.3,
		RateLimit: 100, RetryAfter: time.Second, Seed: 7,
	}, slog.New(slog.DiscardHandler), nil)
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	provider := delivery.NewHTTPProvider(srv.URL, 2*time.Second)

	run := func(maxAttempts int) {
		t.Helper()
		cfg := delivery.DefaultConfig()
		cfg.BatchSize = 50
		cfg.PollInterval = 20 * time.Millisecond
		cfg.Retry.Base, cfg.Retry.Cap = 10*time.Millisecond, 50*time.Millisecond
		cfg.Retry.MaxAttempts = maxAttempts
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			delivery.New(delivery.NewPGStore(pool), provider, cfg, slog.New(slog.DiscardHandler), nil, nil, nil).Run(ctx)
			close(done)
		}()
		defer func() { cancel(); <-done }()
		deadline := time.Now().Add(time.Minute)
		for statusOf(t, pool, c.ID) != StatusCompleted {
			if time.Now().After(deadline) {
				t.Fatalf("campaign not completed: %d open rows", countRows(t, pool, c.ID, `status IN ('pending', 'sending')`))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Run 1: one attempt per row.
	run(1)
	failed := countRows(t, pool, c.ID, `status = 'failed'`)
	sent1 := countRows(t, pool, c.ID, `status = 'sent'`)
	delivered1 := mock.Stats()[mockprovider.ChannelSMS].Delivered + mock.Stats()[mockprovider.ChannelEmail].Delivered
	// Accepted-then-failed rows: delivered at the provider, failed in the db.
	if failed == 0 || delivered1 <= int64(sent1) {
		t.Fatalf("run 1 did not set the scene: %d failed, db sent %d, provider delivered %d", failed, sent1, delivered1)
	}
	t.Logf("run 1: %d rows, %d sent, %d failed, provider delivered %d", total, sent1, failed, delivered1)

	if _, n, err := svc.RetryFailed(t.Context(), c.ID); err != nil || n != int64(failed) {
		t.Fatalf("retry: %v, %d rows, want %d", err, n, failed)
	}

	// Run 2: enough attempts that nothing fails again.
	run(10)
	if n := countRows(t, pool, c.ID, `status = 'failed'`); n != 0 {
		t.Fatalf("%d rows failed after the retry", n)
	}
	stats := mock.Stats()
	for _, ch := range []string{mockprovider.ChannelSMS, mockprovider.ChannelEmail} {
		sent := countRows(t, pool, c.ID, `channel = '`+ch+`' AND status = 'sent'`)
		if stats[ch].Delivered != int64(sent) {
			t.Errorf("%s: provider delivered %d messages, db has %d sent rows", ch, stats[ch].Delivered, sent)
		}
	}
	if stats[mockprovider.ChannelSMS].Replayed+stats[mockprovider.ChannelEmail].Replayed == 0 {
		t.Error("no replayed request: the retry did not meet an already delivered message")
	}
	if n := countRows(t, pool, c.ID, `true`); n != total {
		t.Errorf("%d rows, want %d", n, total)
	}
}
