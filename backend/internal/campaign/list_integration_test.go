//go:build integration

package campaign

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
)

// Layer 2: the campaign list and draft deletion on real Postgres.

// approved creates a campaign with ruleJSON and approves it for both
// channels; it returns the campaign and its snapshot.
func approved(t *testing.T, svc *Service, ruleJSON string) (Campaign, Snapshot) {
	t.Helper()
	_, c := draft(t, svc.db, ruleJSON, goodMessage)
	c, snap, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return c, snap
}

// sentStatuses is the list's "Gönderilenler" preset.
var sentStatuses = []string{string(StatusApproved), string(StatusSending), string(StatusCompleted)}

func ids(items []ListItem) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestList(t *testing.T) {
	pool := seededPool(t)
	svc, d1 := draft(t, pool, istanbulRule, goodMessage)
	a1, snap := approved(t, svc, caseRule)
	_, d2 := draft(t, pool, partialRule, goodMessage)

	// One delivery of a1 failed, one was sent: the counts follow statuses.
	if _, err := pool.Exec(t.Context(), `
UPDATE deliveries SET status = CASE WHEN id = (SELECT min(id) FROM deliveries WHERE campaign_id = $1 AND channel = 'sms')
                                    THEN 'failed' ELSE 'sent' END
WHERE id IN (SELECT id FROM deliveries WHERE campaign_id = $1 AND channel = 'sms' ORDER BY id LIMIT 2)`, a1.ID); err != nil {
		t.Fatal(err)
	}

	t.Run("all, newest first", func(t *testing.T) {
		page, err := svc.List(t.Context(), ListQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(page.Items), []int64{d2.ID, a1.ID, d1.ID}; !slices.Equal(got, want) {
			t.Fatalf("ids %v, want %v", got, want)
		}
		if page.NextBefore != 0 {
			t.Fatalf("next %d, want 0 (last page)", page.NextBefore)
		}
	})

	t.Run("delivery counts", func(t *testing.T) {
		page, err := svc.List(t.Context(), ListQuery{Statuses: sentStatuses})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].ID != a1.ID {
			t.Fatalf("sent filter: %v", ids(page.Items))
		}
		it := page.Items[0]
		if it.Status != StatusApproved || it.ApprovedAt == nil {
			t.Fatalf("item %+v", it)
		}
		sms, email := it.Channels["sms"], it.Channels["email"]
		if sms.Total != snap.SMS || sms.Sent != 1 || sms.Failed != 1 || sms.Pending != snap.SMS-2 {
			t.Fatalf("sms %+v, snapshot %d", sms, snap.SMS)
		}
		if email.Total != snap.Email || email.Pending != snap.Email {
			t.Fatalf("email %+v, snapshot %d", email, snap.Email)
		}
	})

	t.Run("drafts have no counts", func(t *testing.T) {
		page, err := svc.List(t.Context(), ListQuery{Statuses: []string{ListDraft}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(page.Items), []int64{d2.ID, d1.ID}; !slices.Equal(got, want) {
			t.Fatalf("ids %v, want %v", got, want)
		}
		for _, it := range page.Items {
			if len(it.Channels) != 0 || it.ApprovedAt != nil {
				t.Fatalf("draft %+v", it)
			}
		}
	})

	t.Run("keyset pages", func(t *testing.T) {
		first, err := svc.List(t.Context(), ListQuery{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(first.Items); !slices.Equal(got, []int64{d2.ID, a1.ID}) || first.NextBefore != a1.ID {
			t.Fatalf("first page %v next %d", got, first.NextBefore)
		}
		// A campaign created between pages does not shift the next page.
		_, _ = draft(t, pool, istanbulRule, goodMessage)
		second, err := svc.List(t.Context(), ListQuery{Before: first.NextBefore, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(second.Items); !slices.Equal(got, []int64{d1.ID}) || second.NextBefore != 0 {
			t.Fatalf("second page %v next %d", got, second.NextBefore)
		}
	})

	t.Run("limit is clamped", func(t *testing.T) {
		page, err := svc.List(t.Context(), ListQuery{Limit: 10_000})
		if err != nil || len(page.Items) != 4 {
			t.Fatalf("items %d err %v", len(page.Items), err)
		}
	})

	t.Run("unknown status", func(t *testing.T) {
		if _, err := svc.List(t.Context(), ListQuery{Statuses: []string{"archived"}}); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("err %v, want ErrBadQuery", err)
		}
	})
}

// TestListApprovable: a draft whose brief gave no approvable audience is
// marked in the list (PLAN step 15d); ok and partial drafts are approvable.
func TestListApprovable(t *testing.T) {
	pool := seededPool(t)
	_, ok := draft(t, pool, istanbulRule, goodMessage)
	_, partial := draft(t, pool, partialRule, goodMessage)
	svc := NewService(pool, llm.NewFake(llm.Text(rejectedRule)), func() time.Time { return testNow })
	rejected, _, err := svc.Create(t.Context(), "not a campaign")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	page, err := svc.List(t.Context(), ListQuery{Statuses: []string{ListDraft, ListInvalid}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, it := range page.Items {
		got[it.ID] = it.Approvable
	}
	want := map[int64]bool{ok.ID: true, partial.ID: true, rejected.ID: false}
	if !maps.Equal(got, want) {
		t.Fatalf("approvable %v, want %v", got, want)
	}
}

// TestListFilters: every list status, the campaign number and the date
// range, alone and together. The SQL split of drafts into ListDraft and
// ListInvalid must agree with Approvable, which the UI shows.
func TestListFilters(t *testing.T) {
	pool := seededPool(t)
	svc, ok := draft(t, pool, istanbulRule, goodMessage)
	_, partial := draft(t, pool, partialRule, goodMessage)
	bad := NewService(pool, llm.NewFake(llm.Text(rejectedRule)), func() time.Time { return testNow })
	invalid, _, err := bad.Create(t.Context(), "not a campaign")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sent, _ := approved(t, svc, caseRule)
	at := testNow.Add(24 * time.Hour)
	_, sc := draft(t, pool, caseRule, goodMessage)
	if _, _, err := svc.Approve(t.Context(), sc.ID, ApproveRequest{SMS: true, SendAt: &at}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	_, cc := draft(t, pool, caseRule, goodMessage)
	if _, _, err := svc.Approve(t.Context(), cc.ID, ApproveRequest{SMS: true, SendAt: &at}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := svc.Cancel(t.Context(), cc.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// The list date is the approval time, or the creation time of a draft:
	// move the partial draft and the sent campaign 10 days back.
	old := testNow.AddDate(0, 0, -10)
	if _, err := pool.Exec(t.Context(), `UPDATE campaigns SET created_at = $2 WHERE id = $1`, partial.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE campaigns SET created_at = $2, approved_at = $2 WHERE id = $1`, sent.ID, old); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		q    ListQuery
		want []int64
	}{
		{"all", ListQuery{}, []int64{cc.ID, sc.ID, sent.ID, invalid.ID, partial.ID, ok.ID}},
		{"approvable drafts", ListQuery{Statuses: []string{ListDraft}}, []int64{partial.ID, ok.ID}},
		{"invalid drafts", ListQuery{Statuses: []string{ListInvalid}}, []int64{invalid.ID}},
		{"both kinds of draft", ListQuery{Statuses: []string{ListDraft, ListInvalid}}, []int64{invalid.ID, partial.ID, ok.ID}},
		{"sent preset", ListQuery{Statuses: sentStatuses}, []int64{sent.ID}},
		{"scheduled", ListQuery{Statuses: []string{string(StatusScheduled)}}, []int64{sc.ID}},
		{"cancelled", ListQuery{Statuses: []string{string(StatusCancelled)}}, []int64{cc.ID}},
		{"invalid or cancelled", ListQuery{Statuses: []string{ListInvalid, string(StatusCancelled)}}, []int64{cc.ID, invalid.ID}},
		{"campaign number", ListQuery{ID: sc.ID}, []int64{sc.ID}},
		{"number outside the statuses", ListQuery{ID: sc.ID, Statuses: []string{ListDraft}}, []int64{}},
		{"from", ListQuery{From: testNow.AddDate(0, 0, -1)}, []int64{cc.ID, sc.ID, invalid.ID, ok.ID}},
		{"to", ListQuery{To: testNow.AddDate(0, 0, -1)}, []int64{sent.ID, partial.ID}},
		{"range and status", ListQuery{From: old, To: old.Add(time.Hour), Statuses: []string{ListDraft}}, []int64{partial.ID}},
		{"paged", ListQuery{Statuses: []string{ListDraft, ListInvalid}, Before: invalid.ID}, []int64{partial.ID, ok.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := svc.List(t.Context(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(page.Items); !slices.Equal(got, tt.want) {
				t.Fatalf("ids %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("the draft split agrees with Approvable", func(t *testing.T) {
		for st, want := range map[string]bool{ListDraft: true, ListInvalid: false} {
			page, err := svc.List(t.Context(), ListQuery{Statuses: []string{st}})
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range page.Items {
				if it.Status != StatusDraft || it.Approvable != want {
					t.Errorf("%s: campaign %d status %s approvable %v", st, it.ID, it.Status, it.Approvable)
				}
			}
		}
	})

	t.Run("empty date range", func(t *testing.T) {
		if _, err := svc.List(t.Context(), ListQuery{From: testNow, To: testNow}); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("err %v, want ErrBadQuery", err)
		}
	})
}

func TestDeleteDraft(t *testing.T) {
	pool := seededPool(t)
	svc, d := draft(t, pool, istanbulRule, goodMessage)
	a, snap := approved(t, svc, caseRule)

	if err := svc.DeleteDraft(t.Context(), d.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := svc.Get(t.Context(), d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted draft: %v, want ErrNotFound", err)
	}
	if err := svc.DeleteDraft(t.Context(), d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete again: %v, want ErrNotFound", err)
	}

	// An approved campaign and its snapshot stay.
	if err := svc.DeleteDraft(t.Context(), a.ID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("delete approved: %v, want ErrNotDraft", err)
	}
	if sms, email := deliveryCounts(t, pool, a.ID); sms != snap.SMS || email != snap.Email {
		t.Fatalf("deliveries sms %d email %d after refused delete, snapshot %+v", sms, email, snap)
	}
}

// Approval and deletion of the same draft at once: exactly one wins, and
// the database matches the winner (no deliveries without a campaign, no
// approved campaign without its snapshot).
func TestDeleteDraftRacesApproval(t *testing.T) {
	pool := seededPool(t)
	for range 10 {
		svc, c := draft(t, pool, caseRule, goodMessage)

		var wg sync.WaitGroup
		var approveErr, deleteErr error
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			_, _, approveErr = svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true})
		})
		wg.Go(func() {
			<-start
			deleteErr = svc.DeleteDraft(t.Context(), c.ID)
		})
		close(start)
		wg.Wait()

		sms, email := deliveryCounts(t, pool, c.ID)
		_, getErr := svc.Get(t.Context(), c.ID)
		switch {
		case approveErr == nil && errors.Is(deleteErr, ErrNotDraft):
			if getErr != nil || sms+email == 0 {
				t.Fatalf("approval won but campaign err %v, deliveries %d", getErr, sms+email)
			}
		case deleteErr == nil && errors.Is(approveErr, ErrNotFound):
			if !errors.Is(getErr, ErrNotFound) || sms+email != 0 {
				t.Fatalf("delete won but campaign err %v, deliveries %d", getErr, sms+email)
			}
		default:
			t.Fatalf("approve err %v, delete err %v: want exactly one to win", approveErr, deleteErr)
		}
	}
}
