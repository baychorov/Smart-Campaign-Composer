//go:build integration

package delivery

import (
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// Layer 2: the queue on real Postgres. SKIP LOCKED, leases and the fence
// cannot be checked with a fake (D-ARCH).

// queue is an approved campaign with one delivery per consenting user and
// channel, as campaign.Approve writes it.
type queue struct {
	pool       *pgxpool.Pool
	campaignID int64
	sms, email int
}

func newQueue(t *testing.T, users int) queue {
	t.Helper()
	pool := pgtest.NewDB(t)
	ctx := t.Context()
	if err := seed.Insert(ctx, pool, seed.Generate(seed.DefaultSeed, users, time.Now())); err != nil {
		t.Fatal(err)
	}
	q := queue{pool: pool}
	err := pool.QueryRow(ctx, `
INSERT INTO campaigns (brief, status, rule, sms_text, email_subject, email_body, send_sms, send_email, approved_at)
VALUES ('test', 'approved', '{}', 'Merhaba {{first_name}}, {{city}}''de indirim!', '{{first_name}}, sepetin', 'Gövde', true, true, now())
RETURNING id`).Scan(&q.campaignID)
	if err != nil {
		t.Fatal(err)
	}
	for ch, n := range map[string]*int{"sms": &q.sms, "email": &q.email} {
		tag, err := pool.Exec(ctx, `INSERT INTO deliveries (campaign_id, user_id, channel)
SELECT $1, id, $2 FROM users WHERE CASE $2 WHEN 'sms' THEN sms_opt_in ELSE email_opt_in END`, q.campaignID, ch)
		if err != nil {
			t.Fatal(err)
		}
		*n = int(tag.RowsAffected())
	}
	return q
}

func (q queue) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := q.pool.QueryRow(t.Context(), `SELECT count(*) FROM deliveries WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (q queue) campaignStatus(t *testing.T) string {
	t.Helper()
	var s string
	if err := q.pool.QueryRow(t.Context(), `SELECT status FROM campaigns WHERE id = $1`, q.campaignID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// later is safely after the rows' default next_attempt_at (DB now()).
func later() time.Time { return time.Now().Add(time.Second) }

// TestClaimSkipLocked: concurrent claimers never get the same row.
func TestClaimSkipLocked(t *testing.T) {
	q := newQueue(t, 400)
	s := NewPGStore(q.pool)

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				now := later()
				jobs, err := s.Claim(t.Context(), ChannelSMS, 7, now, now.Add(time.Minute))
				if err != nil {
					t.Error(err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					seen[j.ID]++
					if j.Attempts != 1 || j.Channel != ChannelSMS || j.To == "" || j.SMSText == "" {
						t.Errorf("job %+v", j)
					}
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	for id, n := range seen {
		if n != 1 {
			t.Errorf("row %d claimed %d times", id, n)
		}
	}
	if len(seen) != q.sms {
		t.Fatalf("claimed %d rows, queue has %d", len(seen), q.sms)
	}
	if n := q.count(t, `channel = 'email' AND status = 'pending'`); n != q.email {
		t.Errorf("email rows touched: %d pending of %d", n, q.email)
	}
	if st := q.campaignStatus(t); st != "sending" {
		t.Errorf("campaign %s after the first claim, want sending", st)
	}
}

// TestLeaseAndFence: an expired lease makes the row claimable again, and
// the old claim can no longer store its result.
func TestLeaseAndFence(t *testing.T) {
	q := newQueue(t, 50)
	s := NewPGStore(q.pool)
	now := later()

	first, err := s.Claim(t.Context(), ChannelSMS, 1, now, now.Add(time.Minute))
	if err != nil || len(first) != 1 {
		t.Fatalf("claim: %v %v", first, err)
	}
	// Within the lease, the row is not handed out again.
	again, _ := s.Claim(t.Context(), ChannelSMS, q.sms, now.Add(59*time.Second), now.Add(2*time.Minute))
	for _, j := range again {
		if j.ID == first[0].ID {
			t.Fatal("row re-claimed within its lease")
		}
	}
	// After the lease ("the worker died"), it is.
	var second Job
	retaken, _ := s.Claim(t.Context(), ChannelSMS, q.sms, now.Add(time.Minute), now.Add(3*time.Minute))
	for _, j := range retaken {
		if j.ID == first[0].ID {
			second = j
		}
	}
	if second.ID == 0 || second.Attempts != 2 {
		t.Fatalf("expired lease not re-claimed: %+v", second)
	}

	// The stale claim's result is dropped; the current one is stored.
	if ok, err := s.Complete(t.Context(), first[0], Decision{Kind: KindSent}, now); ok || err != nil {
		t.Fatalf("stale complete: ok %v err %v", ok, err)
	}
	if ok, err := s.Complete(t.Context(), second, Decision{Kind: KindSent}, now); !ok || err != nil {
		t.Fatalf("current complete: ok %v err %v", ok, err)
	}
	if q.count(t, `id = $1 AND status = 'sent' AND sent_at IS NOT NULL AND locked_until IS NULL`, second.ID) != 1 {
		t.Fatal("row not sent")
	}
}

func TestCompleteKinds(t *testing.T) {
	q := newQueue(t, 50)
	s := NewPGStore(q.pool)
	now := later()
	jobs, err := s.Claim(t.Context(), ChannelSMS, 6, now, now.Add(time.Minute))
	if err != nil || len(jobs) != 6 {
		t.Fatalf("claim: %d %v", len(jobs), err)
	}
	decisions := []Decision{
		{Kind: KindSent},
		{Kind: KindFailed, Error: "http 400"},
		{Kind: KindSkipped, Error: "consent withdrawn"},
		{Kind: KindRetry, Delay: 10 * time.Second, Error: "http 500"},
		{Kind: KindRetry, Delay: time.Second, Throttled: true, Error: "http 429"},
		{Kind: KindRelease},
	}
	type row struct {
		status              string
		attempts, throttles int
		next                time.Time
		lastErr             *string
	}
	for i, d := range decisions {
		if ok, err := s.Complete(t.Context(), jobs[i], d, now); !ok || err != nil {
			t.Fatalf("%s: ok %v err %v", d.Kind, ok, err)
		}
	}
	want := []row{
		{"sent", 1, 0, time.Time{}, nil},
		{"failed", 1, 0, time.Time{}, ptr("http 400")},
		{"skipped", 1, 0, time.Time{}, ptr("consent withdrawn")},
		{"pending", 1, 0, now.Add(10 * time.Second), ptr("http 500")},
		{"pending", 0, 1, now.Add(time.Second), ptr("http 429")}, // 429: no attempt used
		{"pending", 0, 0, now, nil},                              // released: no attempt used
	}
	for i, w := range want {
		var got row
		err := q.pool.QueryRow(t.Context(), `SELECT status, attempts, throttles, next_attempt_at, last_error FROM deliveries WHERE id = $1`,
			jobs[i].ID).Scan(&got.status, &got.attempts, &got.throttles, &got.next, &got.lastErr)
		if err != nil {
			t.Fatal(err)
		}
		if got.status != w.status || got.attempts != w.attempts || got.throttles != w.throttles ||
			(!w.next.IsZero() && !got.next.Equal(w.next.Truncate(time.Microsecond))) || deref(got.lastErr) != deref(w.lastErr) {
			t.Errorf("%s: got %+v (last error %q), want %+v", decisions[i].Kind, got, deref(got.lastErr), w)
		}
	}
}

func ptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestConsentReadAtClaim: consent withdrawn after approval is seen by the
// claim (R-AUD-2).
func TestConsentReadAtClaim(t *testing.T) {
	q := newQueue(t, 50)
	var userID int64
	if err := q.pool.QueryRow(t.Context(), `
UPDATE users SET sms_opt_in = false
WHERE id = (SELECT user_id FROM deliveries WHERE channel = 'sms' ORDER BY id LIMIT 1)
RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	now := later()
	jobs, err := NewPGStore(q.pool).Claim(t.Context(), ChannelSMS, q.sms, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := 0
	for _, j := range jobs {
		if !j.Consent {
			withdrawn++
		}
	}
	if withdrawn != 1 {
		t.Fatalf("%d jobs without consent, want 1", withdrawn)
	}
}

func TestFinishCampaigns(t *testing.T) {
	q := newQueue(t, 30)
	s := NewPGStore(q.pool)
	now := later()

	finish := func() {
		t.Helper()
		if _, err := s.FinishCampaigns(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	finish()
	if st := q.campaignStatus(t); st != "approved" {
		t.Fatalf("untouched campaign %s, want approved", st)
	}
	for _, ch := range Channels {
		jobs, err := s.Claim(t.Context(), ch, 1000, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		for i, j := range jobs {
			if i == 0 && ch == ChannelEmail {
				continue // one row stays open
			}
			if _, err := s.Complete(t.Context(), j, Decision{Kind: KindSent}, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	finish()
	if st := q.campaignStatus(t); st != "sending" {
		t.Fatalf("campaign with an open row is %s, want sending", st)
	}
	if _, err := q.pool.Exec(t.Context(), `UPDATE deliveries SET status = 'failed' WHERE status = 'sending'`); err != nil {
		t.Fatal(err)
	}
	finish()
	if st := q.campaignStatus(t); st != "completed" {
		t.Fatalf("campaign %s, want completed", st)
	}
}
