//go:build integration

package campaign

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/delivery"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
)

// Layer 2: scheduled sending (PLAN step 15a) on real Postgres, with the
// worker's real claim query (delivery.PGStore).

// scheduled creates a draft and approves it for both channels at sendAt.
func scheduled(t *testing.T, pool *pgxpool.Pool, sendAt time.Time) (*Service, Campaign, Snapshot) {
	t.Helper()
	svc, c := draft(t, pool, caseRule, goodMessage)
	c, snap, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true, SendAt: &sendAt})
	if err != nil {
		t.Fatalf("approve scheduled: %v", err)
	}
	return svc, c, snap
}

func claimAll(t *testing.T, pool *pgxpool.Pool, now time.Time) int {
	t.Helper()
	store := delivery.NewPGStore(pool)
	n := 0
	for _, ch := range []delivery.Channel{delivery.ChannelSMS, delivery.ChannelEmail} {
		jobs, err := store.Claim(t.Context(), ch, 10_000, now, now.Add(time.Minute))
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		n += len(jobs)
	}
	return n
}

func statusOf(t *testing.T, pool *pgxpool.Pool, id int64) Status {
	t.Helper()
	var s Status
	if err := pool.QueryRow(t.Context(), `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApproveScheduled(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(2 * time.Hour)
	_, c, snap := scheduled(t, pool, sendAt)

	if c.Status != StatusScheduled || c.ScheduledAt == nil || !c.ScheduledAt.Equal(sendAt) {
		t.Fatalf("campaign %+v", c)
	}
	// The snapshot is taken now; every row waits for the send time.
	var rows, due int64
	var first time.Time
	if err := pool.QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (WHERE next_attempt_at = $2), min(next_attempt_at)
FROM deliveries WHERE campaign_id = $1`, c.ID, sendAt).Scan(&rows, &due, &first); err != nil {
		t.Fatal(err)
	}
	if rows != snap.SMS+snap.Email || rows == 0 || due != rows {
		t.Fatalf("%d rows, %d due at the send time, first %v; snapshot %+v", rows, due, first, snap)
	}

	// Not claimed a second before the send time; claimed at it.
	if n := claimAll(t, pool, sendAt.Add(-time.Second)); n != 0 {
		t.Fatalf("claimed %d rows before the send time", n)
	}
	if statusOf(t, pool, c.ID) != StatusScheduled {
		t.Fatal("status changed before the send time")
	}
	if n := claimAll(t, pool, sendAt); n != int(rows) {
		t.Fatalf("claimed %d of %d rows at the send time", n, rows)
	}
	if s := statusOf(t, pool, c.ID); s != StatusSending {
		t.Fatalf("status %s after the first claim, want sending", s)
	}
}

func TestApproveSendAtLimits(t *testing.T) {
	pool := seededPool(t)
	for name, at := range map[string]time.Time{
		"too soon":    testNow.Add(MinScheduleLead - time.Second),
		"in the past": testNow.Add(-time.Hour),
		"too far":     testNow.Add(MaxScheduleLead + time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			svc, c := draft(t, pool, caseRule, goodMessage)
			_, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true, SendAt: &at})
			if got := approvalCodes(t, err); len(got) != 1 || got[0] != CodeInvalidSendAt {
				t.Fatalf("codes %v, want [%s]", got, CodeInvalidSendAt)
			}
			if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 || statusOf(t, pool, c.ID) != StatusDraft {
				t.Fatal("a refused approval wrote something")
			}
		})
	}
	// The limits themselves are accepted.
	for _, at := range []time.Time{testNow.Add(MinScheduleLead), testNow.Add(MaxScheduleLead)} {
		svc, c := draft(t, pool, caseRule, goodMessage)
		if _, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, SendAt: &at}); err != nil {
			t.Errorf("send at %v: %v", at, err)
		}
	}
}

func TestCreateKeepsSuggestedSendAt(t *testing.T) {
	pool := seededPool(t)
	withSend := func(v string) string {
		return strings.Replace(istanbulRule, `"notes":[]`, `"notes":[],"send_at":`+v, 1)
	}

	tests := []struct {
		name     string
		reply    string
		want     *time.Time
		wantWarn bool
	}{
		{"no send time", withSend("null"), nil, false},
		{"tomorrow 10:00", withSend(`"2026-06-16T10:00:00+03:00"`), ptrTime(time.Date(2026, 6, 16, 7, 0, 0, 0, time.UTC)), false},
		{"in the past is dropped", withSend(`"2026-06-01T10:00:00+03:00"`), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(pool, llm.NewFake(llm.Text(tt.reply)), func() time.Time { return testNow })
			c, res, err := svc.Create(t.Context(), "brief")
			if err != nil {
				t.Fatal(err)
			}
			if (c.ScheduledAt == nil) != (tt.want == nil) || (tt.want != nil && !c.ScheduledAt.Equal(*tt.want)) {
				t.Errorf("suggestion %v, want %v", c.ScheduledAt, tt.want)
			}
			warned := false
			for _, w := range res.Validation.Warnings {
				warned = warned || w.Code == CodeInvalidSendAt
			}
			if warned != tt.wantWarn {
				t.Errorf("warning = %v, want %v", warned, tt.wantWarn)
			}
			if c.Status != StatusDraft {
				t.Errorf("a suggestion must not schedule anything: status %s", c.Status)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestCancel(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(time.Hour)

	svc, c, _ := scheduled(t, pool, sendAt)
	got, err := svc.Cancel(t.Context(), c.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("cancel: %v, status %s", err, got.Status)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 {
		t.Fatalf("%d deliveries left after cancel", sms+email)
	}
	if n := claimAll(t, pool, sendAt.Add(time.Hour)); n != 0 {
		t.Fatalf("a cancelled campaign sent %d messages", n)
	}
	if _, err := svc.Cancel(t.Context(), c.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("cancel twice: %v", err)
	}

	// Sending now, already sending, and missing campaigns cannot be cancelled.
	now, _ := approved(t, svc, caseRule)
	if _, err := svc.Cancel(t.Context(), now.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("cancel an approved campaign: %v", err)
	}
	_, started, _ := scheduled(t, pool, sendAt)
	claimAll(t, pool, sendAt)
	if _, err := svc.Cancel(t.Context(), started.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("cancel after sending started: %v", err)
	}
	if _, err := svc.Cancel(t.Context(), 999_999); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancel a missing campaign: %v", err)
	}
}

// TestCancelConcurrent: many cancels of one scheduled campaign at the same
// time, on a pool smaller than the number of callers (see
// TestApproveConcurrent). Exactly one wins; the rest get ErrNotScheduled.
func TestCancelConcurrent(t *testing.T) {
	pool := seededPool(t)
	_, c, _ := scheduled(t, pool, testNow.Add(time.Hour))
	svc := NewService(smallPool(t, pool, 2), llm.NewFake(), func() time.Time { return testNow })

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Cancel(t.Context(), c.ID)
		})
	}
	close(start)
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrNotScheduled):
		default:
			t.Errorf("cancel %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d cancels succeeded, want exactly 1", wins)
	}
	if got := statusOf(t, pool, c.ID); got != StatusCancelled {
		t.Fatalf("status %s, want cancelled", got)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 {
		t.Fatalf("%d deliveries left after cancel", sms+email)
	}
}

// The two orders of the race, made deterministic by holding a row lock in
// a transaction of our own.
func TestCancelWhileWorkerHoldsRows(t *testing.T) {
	pool := seededPool(t)
	svc, c, snap := scheduled(t, pool, testNow.Add(time.Hour))

	// A worker in the middle of a claim holds one of the rows.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT id FROM deliveries WHERE campaign_id = $1 LIMIT 1 FOR UPDATE`, c.ID); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := svc.Cancel(t.Context(), c.ID); !errors.Is(err, ErrSendingStarted) {
		t.Fatalf("cancel: %v, want ErrSendingStarted", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("cancel waited %v; it must not wait for the worker", d)
	}
	// Nothing changed: sending wins.
	if s := statusOf(t, pool, c.ID); s != StatusScheduled {
		t.Errorf("status %s after a refused cancel", s)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != snap.Email {
		t.Errorf("deliveries changed after a refused cancel")
	}
}

func TestWorkerSkipsRowsCancelHolds(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(time.Hour)
	_, c, _ := scheduled(t, pool, sendAt)

	// A cancel in progress holds every row.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT id FROM deliveries WHERE campaign_id = $1 FOR UPDATE NOWAIT`, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := claimAll(t, pool, sendAt); n != 0 {
		t.Fatalf("worker claimed %d rows held by a cancel", n)
	}
}

// Cancel and claim at the send time, many rounds: exactly one wins each
// round and the database matches the winner.
func TestCancelRacesClaim(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(time.Hour)
	outcomes := map[string]int{}
	for range 20 {
		svc, c, snap := scheduled(t, pool, sendAt)

		var wg sync.WaitGroup
		var cancelErr error
		claimed := 0
		begin := make(chan struct{})
		wg.Go(func() {
			<-begin
			_, cancelErr = svc.Cancel(t.Context(), c.ID)
		})
		wg.Go(func() {
			<-begin
			claimed = claimAll(t, pool, sendAt)
		})
		close(begin)
		wg.Wait()

		sms, email := deliveryCounts(t, pool, c.ID)
		status := statusOf(t, pool, c.ID)
		switch {
		case cancelErr == nil:
			outcomes["cancel"]++
			if claimed != 0 || sms+email != 0 || status != StatusCancelled {
				t.Fatalf("cancel won but claimed %d, %d rows left, status %s", claimed, sms+email, status)
			}
		case errors.Is(cancelErr, ErrSendingStarted) || errors.Is(cancelErr, ErrNotScheduled):
			outcomes["send"]++
			if claimed == 0 || sms != snap.SMS || email != snap.Email || status != StatusSending {
				t.Fatalf("sending won (%v) but claimed %d, rows %d/%d, status %s", cancelErr, claimed, sms, email, status)
			}
		default:
			t.Fatalf("cancel: %v", cancelErr)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}
