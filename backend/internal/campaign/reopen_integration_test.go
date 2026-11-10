//go:build integration

package campaign

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Layer 2: editing a scheduled campaign (Reopen) on real Postgres. It shares
// Cancel's locking (unschedule), so the same races are checked for it.

func TestReopen(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(time.Hour)
	svc, c, _ := scheduled(t, pool, sendAt)
	if _, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true}); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("approve a scheduled campaign: %v, want ErrNotDraft", err)
	}

	got, err := svc.Reopen(t.Context(), c.ID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got.Status != StatusDraft || got.ApprovedAt != nil || got.PartialAckAt != nil {
		t.Fatalf("reopened: status %s, approved_at %v, partial_ack_at %v", got.Status, got.ApprovedAt, got.PartialAckAt)
	}
	if got.ScheduledAt == nil || !got.ScheduledAt.Equal(sendAt) || !got.SendSMS || !got.SendEmail {
		t.Fatalf("reopened draft lost its send time or channels: %v sms %v email %v", got.ScheduledAt, got.SendSMS, got.SendEmail)
	}
	if got.Message != goodMessage {
		t.Fatalf("message changed: %+v", got.Message)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 {
		t.Fatalf("%d deliveries left after reopen", sms+email)
	}
	if n := claimAll(t, pool, sendAt.Add(time.Hour)); n != 0 {
		t.Fatalf("a reopened draft sent %d messages", n)
	}
	if _, err := svc.Reopen(t.Context(), c.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("reopen a draft: %v", err)
	}

	// It is an ordinary draft again: edited, then approved for a new time
	// with a new snapshot whose rows are due at the new time only.
	edited := goodMessage
	edited.SMS = "Merhaba {{first_name}}! Yeni fırsat: SEPET20 ile %20 indirim."
	if _, _, err := svc.SaveContent(t.Context(), c.ID, edited); err != nil {
		t.Fatalf("edit the reopened draft: %v", err)
	}
	later := sendAt.Add(24 * time.Hour)
	again, snap, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, SendAt: &later})
	if err != nil {
		t.Fatalf("approve again: %v", err)
	}
	if again.Status != StatusScheduled || !again.ScheduledAt.Equal(later) || again.Message.SMS != edited.SMS || again.SendEmail {
		t.Fatalf("approved again: %+v", again)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != 0 || sms == 0 {
		t.Fatalf("deliveries sms %d email %d, snapshot %+v", sms, email, snap)
	}
	if n := claimAll(t, pool, sendAt); n != 0 {
		t.Fatalf("%d rows due at the old send time", n)
	}
	if n := claimAll(t, pool, later); n != int(snap.SMS) {
		t.Fatalf("claimed %d of %d rows at the new send time", n, snap.SMS)
	}

	// Sending now, already sending, cancelled and missing campaigns cannot
	// be reopened.
	now, _ := approved(t, svc, caseRule)
	if _, err := svc.Reopen(t.Context(), now.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("reopen an approved campaign: %v", err)
	}
	if _, err := svc.Reopen(t.Context(), c.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("reopen after sending started: %v", err)
	}
	_, cancelled, _ := scheduled(t, pool, sendAt)
	if _, err := svc.Cancel(t.Context(), cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reopen(t.Context(), cancelled.ID); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("reopen a cancelled campaign: %v", err)
	}
	if _, err := svc.Reopen(t.Context(), 999_999); !errors.Is(err, ErrNotFound) {
		t.Errorf("reopen a missing campaign: %v", err)
	}
}

// The "continue without the criteria" acknowledgement belongs to an
// approval; a reopened partial draft must be acknowledged again.
func TestReopenNeedsPartialAckAgain(t *testing.T) {
	pool := seededPool(t)
	svc, c := draft(t, pool, partialRule, goodMessage)
	sendAt := testNow.Add(time.Hour)
	c, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true, PartialAck: true, SendAt: &sendAt})
	if err != nil || c.PartialAckAt == nil {
		t.Fatalf("approve partial: %v, ack %v", err, c.PartialAckAt)
	}
	if c, err = svc.Reopen(t.Context(), c.ID); err != nil || c.PartialAckAt != nil {
		t.Fatalf("reopen: %v, ack %v", err, c.PartialAckAt)
	}
	_, _, err = svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true, SendAt: &sendAt})
	if got := approvalCodes(t, err); len(got) != 1 || got[0] != CodePartialNotAcked {
		t.Fatalf("codes %v, want [%s]", got, CodePartialNotAcked)
	}
}

func TestReopenWhileWorkerHoldsRows(t *testing.T) {
	pool := seededPool(t)
	svc, c, snap := scheduled(t, pool, testNow.Add(time.Hour))

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT id FROM deliveries WHERE campaign_id = $1 LIMIT 1 FOR UPDATE`, c.ID); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := svc.Reopen(t.Context(), c.ID); !errors.Is(err, ErrSendingStarted) {
		t.Fatalf("reopen: %v, want ErrSendingStarted", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("reopen waited %v; it must not wait for the worker", d)
	}
	got, err := svc.Get(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusScheduled || got.ApprovedAt == nil {
		t.Errorf("after a refused reopen: status %s, approved_at %v", got.Status, got.ApprovedAt)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != snap.Email {
		t.Errorf("deliveries changed after a refused reopen")
	}
}

// Reopen and claim at the send time, many rounds: exactly one wins each
// round and the database matches the winner. A reopened draft has no rows
// and is not approved; a sent campaign keeps its whole snapshot.
func TestReopenRacesClaim(t *testing.T) {
	pool := seededPool(t)
	sendAt := testNow.Add(time.Hour)
	outcomes := map[string]int{}
	for range 20 {
		svc, c, snap := scheduled(t, pool, sendAt)

		var wg sync.WaitGroup
		var reopenErr error
		claimed := 0
		begin := make(chan struct{})
		wg.Go(func() {
			<-begin
			_, reopenErr = svc.Reopen(t.Context(), c.ID)
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
		case reopenErr == nil:
			outcomes["reopen"]++
			if claimed != 0 || sms+email != 0 || status != StatusDraft {
				t.Fatalf("reopen won but claimed %d, %d rows left, status %s", claimed, sms+email, status)
			}
		case errors.Is(reopenErr, ErrSendingStarted) || errors.Is(reopenErr, ErrNotScheduled):
			outcomes["send"]++
			if claimed == 0 || sms != snap.SMS || email != snap.Email || status != StatusSending {
				t.Fatalf("sending won (%v) but claimed %d, rows %d/%d, status %s", reopenErr, claimed, sms, email, status)
			}
		default:
			t.Fatalf("reopen: %v", reopenErr)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// Edit and cancel clicked at once (two tabs): exactly one wins.
func TestReopenRacesCancel(t *testing.T) {
	pool := seededPool(t)
	for range 10 {
		svc, c, _ := scheduled(t, pool, testNow.Add(time.Hour))

		var wg sync.WaitGroup
		var reopenErr, cancelErr error
		begin := make(chan struct{})
		wg.Go(func() {
			<-begin
			_, reopenErr = svc.Reopen(t.Context(), c.ID)
		})
		wg.Go(func() {
			<-begin
			_, cancelErr = svc.Cancel(t.Context(), c.ID)
		})
		close(begin)
		wg.Wait()

		status := statusOf(t, pool, c.ID)
		switch {
		case reopenErr == nil && errors.Is(cancelErr, ErrNotScheduled):
			if status != StatusDraft {
				t.Fatalf("reopen won, status %s", status)
			}
		case cancelErr == nil && errors.Is(reopenErr, ErrNotScheduled):
			if status != StatusCancelled {
				t.Fatalf("cancel won, status %s", status)
			}
		default:
			t.Fatalf("reopen err %v, cancel err %v: want exactly one to win", reopenErr, cancelErr)
		}
		if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 {
			t.Fatalf("%d deliveries left", sms+email)
		}
	}
}
