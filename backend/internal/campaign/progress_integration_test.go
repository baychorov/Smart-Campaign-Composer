//go:build integration

package campaign

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// TestProgress: the dashboard summary of a campaign in the middle of
// sending. Row states are set directly, as the worker would leave them.
func TestProgress(t *testing.T) {
	pool := seededPool(t)
	svc, c := draft(t, pool, caseRule, goodMessage)
	if _, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true}); err != nil {
		t.Fatal(err)
	}
	snapSMS, snapEmail := deliveryCounts(t, pool, c.ID)
	if snapSMS < 6 || snapEmail < 2 {
		t.Fatalf("audience too small for the test: sms %d email %d", snapSMS, snapEmail)
	}

	// SMS rows, in id order: 3 sent (2 within the rate window), 1 failed,
	// 1 waiting for a retry, 1 skipped; the rest pending. Email: 1 sending.
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	setNth := func(ch string, n int, set string, args ...any) {
		t.Helper()
		exec(`UPDATE deliveries SET `+set+` WHERE id = (SELECT id FROM deliveries
WHERE campaign_id = $1 AND channel = $2 ORDER BY id OFFSET $3 LIMIT 1)`, append([]any{c.ID, ch, n}, args...)...)
	}
	setNth("sms", 0, `status = 'sent', sent_at = $4`, testNow.Add(-2*time.Second))
	setNth("sms", 1, `status = 'sent', sent_at = $4`, testNow.Add(-9*time.Second))
	setNth("sms", 2, `status = 'sent', sent_at = $4, last_error = NULL`, testNow.Add(-time.Minute))
	setNth("sms", 3, `status = 'failed', last_error = 'http 400'`)
	setNth("sms", 4, `status = 'pending', last_error = 'http 503'`)
	setNth("sms", 5, `status = 'skipped', last_error = 'consent withdrawn'`)
	setNth("email", 0, `status = 'sending', last_error = 'http 503'`)
	exec(`UPDATE campaigns SET status = 'sending' WHERE id = $1`, c.ID)

	p, err := svc.Progress(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantSMS := ChannelProgress{Total: snapSMS, Pending: snapSMS - 5, Sent: 3, Failed: 1, Skipped: 1}
	wantEmail := ChannelProgress{Total: snapEmail, Pending: snapEmail - 1, Sending: 1}
	if p.Channels["sms"] != wantSMS || p.Channels["email"] != wantEmail {
		t.Fatalf("channels %+v, want sms %+v email %+v", p.Channels, wantSMS, wantEmail)
	}
	if p.Status != StatusSending || p.Done || !p.At.Equal(testNow) {
		t.Fatalf("status %s done %v at %s", p.Status, p.Done, p.At)
	}
	// Two sent within the last 10 s.
	if p.RatePerSecond != 0.2 {
		t.Errorf("rate %v, want 0.2", p.RatePerSecond)
	}
	wantErrors := []ErrorCount{{"http 503", 2}, {"consent withdrawn", 1}, {"http 400", 1}}
	if !slices.Equal(p.TopErrors, wantErrors) {
		t.Errorf("top errors %+v, want %+v", p.TopErrors, wantErrors)
	}

	// No recipient data in the summary.
	b, _ := json.Marshal(p)
	for _, leak := range []string{"@", "+90", seed.LongestFirstName, "Deneme"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("progress JSON contains %q: %s", leak, b)
		}
	}

	exec(`UPDATE campaigns SET status = 'completed' WHERE id = $1`, c.ID)
	if p, err = svc.Progress(t.Context(), c.ID); err != nil || !p.Done {
		t.Fatalf("completed campaign: done %v, err %v", p.Done, err)
	}
	if _, err := svc.Progress(t.Context(), 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown campaign: %v", err)
	}
}
