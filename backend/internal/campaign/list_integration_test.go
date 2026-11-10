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
		page, err := svc.List(t.Context(), FilterAll, 0, 0)
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
		page, err := svc.List(t.Context(), FilterSent, 0, 0)
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
		page, err := svc.List(t.Context(), FilterDraft, 0, 0)
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
		first, err := svc.List(t.Context(), FilterAll, 0, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(first.Items); !slices.Equal(got, []int64{d2.ID, a1.ID}) || first.NextBefore != a1.ID {
			t.Fatalf("first page %v next %d", got, first.NextBefore)
		}
		// A campaign created between pages does not shift the next page.
		_, _ = draft(t, pool, istanbulRule, goodMessage)
		second, err := svc.List(t.Context(), FilterAll, first.NextBefore, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(second.Items); !slices.Equal(got, []int64{d1.ID}) || second.NextBefore != 0 {
			t.Fatalf("second page %v next %d", got, second.NextBefore)
		}
	})

	t.Run("limit is clamped", func(t *testing.T) {
		page, err := svc.List(t.Context(), FilterAll, 0, 10_000)
		if err != nil || len(page.Items) != 4 {
			t.Fatalf("items %d err %v", len(page.Items), err)
		}
	})

	t.Run("unknown filter", func(t *testing.T) {
		if _, err := svc.List(t.Context(), "archived", 0, 0); !errors.Is(err, ErrBadFilter) {
			t.Fatalf("err %v, want ErrBadFilter", err)
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

	page, err := svc.List(t.Context(), FilterDraft, 0, 0)
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
