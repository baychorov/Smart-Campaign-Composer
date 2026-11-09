//go:build integration

package campaign

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// Layer 2: approval against real Postgres with the seed dataset. The LLM
// is a Fake that returns a hand-written rule.

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// Rules as the model would return them.
const (
	// The case brief, interpreted per D-CART.
	caseRule = `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İstanbul"]},{"type":"event","event":"add_to_cart","occurred":true,"within_days":30},{"type":"event","event":"purchase","occurred":false,"within_days":30}],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
	// Every İstanbul user; it includes the longest-name cohort.
	istanbulRule = `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İstanbul"]}],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
	// Test set #11: partial.
	partialRule = `{"status":"partial","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İzmir"]}],"groups":[],"assumptions":[],"unsupported":[{"text":"en az 3 kez alışveriş","reason":"aggregate","group":null}],"notes":[]}`
	// Test set #14: rejected.
	rejectedRule = `{"status":"rejected","audience_scope":"filtered","common":[],"groups":[],"assumptions":[],"unsupported":[{"text":"sıralama algoritması","reason":"off_topic","group":null}],"notes":[]}`
	// A real province with no seed users.
	emptyRule = `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["Hakkari"]}],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
)

var goodMessage = content.Message{
	SMS:          "Merhaba {{first_name}}! Sepetindeki ürünler seni bekliyor: SEPET10 ile %10 indirim.",
	EmailSubject: "{{first_name}}, sepetin seni bekliyor",
	EmailBody:    "Merhaba {{first_name}},\n\nSEPET10 koduyla %10 indirim.",
}

func seededPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.NewDB(t)
	if err := seed.Insert(t.Context(), pool, seed.Generate(seed.DefaultSeed, 2000, testNow)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool
}

// smallPool opens a second pool on pool's database with at most maxConns
// connections. pgxpool's default size is max(4, NumCPU), so concurrency
// tests that need a second connection per caller pass on a many-core
// machine and deadlock on a 2-core CI runner; a fixed small pool makes
// that failure show up everywhere.
func smallPool(t *testing.T, pool *pgxpool.Pool, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config()
	cfg.MaxConns = maxConns
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("small pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// draft creates a campaign whose rule is ruleJSON and saves msg into it.
func draft(t *testing.T, pool *pgxpool.Pool, ruleJSON string, msg content.Message) (*Service, Campaign) {
	t.Helper()
	svc := NewService(pool, llm.NewFake(llm.Text(ruleJSON)), func() time.Time { return testNow })
	c, _, err := svc.Create(t.Context(), "test brief")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Status != StatusDraft || !c.SendSMS || !c.SendEmail {
		t.Fatalf("new draft: status %s, sms %v, email %v", c.Status, c.SendSMS, c.SendEmail)
	}
	if c, _, err = svc.SaveContent(t.Context(), c.ID, msg); err != nil {
		t.Fatalf("save content: %v", err)
	}
	return svc, c
}

func deliveryCounts(t *testing.T, pool *pgxpool.Pool, id int64) (sms, email int64) {
	t.Helper()
	err := pool.QueryRow(t.Context(), `
SELECT count(*) FILTER (WHERE channel = 'sms'), count(*) FILTER (WHERE channel = 'email')
FROM deliveries WHERE campaign_id = $1`, id).Scan(&sms, &email)
	if err != nil {
		t.Fatal(err)
	}
	return sms, email
}

// cohortChannels returns the channels a planted cohort user got.
func cohortChannels(t *testing.T, pool *pgxpool.Pool, id int64, cohort string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
SELECT d.channel FROM deliveries d JOIN users u ON u.id = d.user_id
WHERE d.campaign_id = $1 AND u.email = $2 ORDER BY d.channel`, id, seed.CohortEmail(cohort))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			t.Fatal(err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func approvalCodes(t *testing.T, err error) []string {
	t.Helper()
	var ae *ApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("want *ApprovalError, got %v", err)
	}
	return codes(ae.Issues)
}

func TestApproveWritesSnapshot(t *testing.T) {
	pool := seededPool(t)
	svc, c := draft(t, pool, caseRule, goodMessage)

	// The counts the marketer sees before approval.
	before, ok, err := svc.Audience(t.Context(), c)
	if err != nil || !ok || before.Snapshot || before.SMS == 0 || before.Email == 0 {
		t.Fatalf("draft audience %+v, ok %v, err %v", before, ok, err)
	}

	got, snap, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.Status != StatusApproved || got.ApprovedAt == nil || !got.ApprovedAt.Equal(testNow) || got.PartialAckAt != nil {
		t.Fatalf("approved campaign %+v", got)
	}
	// The snapshot matches what was shown: same rule, same anchor time.
	if snap.SMS != before.SMS || snap.Email != before.Email {
		t.Fatalf("snapshot %+v, draft showed sms %d email %d", snap, before.SMS, before.Email)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != snap.Email {
		t.Fatalf("deliveries sms %d email %d, snapshot %+v", sms, email, snap)
	}
	after, _, err := svc.Audience(t.Context(), got)
	if err != nil || !after.Snapshot || after.SMS != snap.SMS || after.Email != snap.Email || after.Reachable != before.Reachable {
		t.Fatalf("approved audience %+v, err %v", after, err)
	}

	// R-AUD-2: no delivery on a channel the user has not consented to.
	var violations int
	err = pool.QueryRow(t.Context(), `
SELECT count(*) FROM deliveries d JOIN users u ON u.id = d.user_id
WHERE d.campaign_id = $1
  AND ((d.channel = 'sms' AND NOT u.sms_opt_in) OR (d.channel = 'email' AND NOT u.email_opt_in))`, c.ID).Scan(&violations)
	if err != nil || violations != 0 {
		t.Fatalf("%d deliveries without consent (err %v)", violations, err)
	}

	// Planted cohorts land exactly where D-CART and consent say.
	for cohort, want := range map[string][]string{
		seed.CohortCaseTarget:           {"email", "sms"},
		seed.CohortCaseWindowInside:     {"email", "sms"},
		seed.CohortCaseEmailOnly:        {"email"},
		seed.CohortCaseNoConsent:        nil,
		seed.CohortCasePurchaseThenCart: nil,
		seed.CohortCaseWindowOutside:    nil,
		seed.CohortCaseOtherCity:        nil,
	} {
		if got := cohortChannels(t, pool, c.ID, cohort); !slices.Equal(got, want) {
			t.Errorf("cohort %s got %v, want %v", cohort, got, want)
		}
	}

	// An approved campaign is frozen.
	if _, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true}); !errors.Is(err, ErrNotDraft) {
		t.Errorf("second approve: %v", err)
	}
	if _, _, err := svc.SaveContent(t.Context(), c.ID, goodMessage); !errors.Is(err, ErrNotDraft) {
		t.Errorf("edit content after approval: %v", err)
	}
	if _, _, err := svc.UpdateRule(t.Context(), c.ID, c.Rule); !errors.Is(err, ErrNotDraft) {
		t.Errorf("edit rule after approval: %v", err)
	}
	if _, _, err := svc.Approve(t.Context(), 999999, ApproveRequest{SMS: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown campaign: %v", err)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != snap.Email {
		t.Fatalf("deliveries changed after a refused approval: sms %d email %d", sms, email)
	}
}

// TestApproveConcurrent: many approvals of one draft at the same time, like
// a double click. Exactly one wins; the rest get ErrNotDraft and write
// nothing (D-DEDUP layer 1).
//
// The service gets a pool smaller than the number of callers: a loser that
// needed a second connection while holding its transaction's one would
// deadlock the pool (seen on a 2-core CI runner, PLAN step 15b).
func TestApproveConcurrent(t *testing.T) {
	pool := seededPool(t)
	_, c := draft(t, pool, caseRule, goodMessage)
	svc := NewService(smallPool(t, pool, 2), llm.NewFake(), func() time.Time { return testNow })

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	snaps := make([]Snapshot, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, snaps[i], errs[i] = svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true})
		})
	}
	close(start)
	wg.Wait()

	wins := 0
	var snap Snapshot
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
			snap = snaps[i]
		case errors.Is(err, ErrNotDraft):
		default:
			t.Errorf("approval %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d approvals succeeded, want exactly 1", wins)
	}
	if sms, email := deliveryCounts(t, pool, c.ID); sms != snap.SMS || email != snap.Email {
		t.Fatalf("deliveries sms %d email %d, winning snapshot %+v", sms, email, snap)
	}
}

func TestApproveChannels(t *testing.T) {
	pool := seededPool(t)
	// SMS only: email copy may be empty, no email rows are written.
	smsOnly := goodMessage
	smsOnly.EmailSubject, smsOnly.EmailBody = "", ""
	svc, c := draft(t, pool, caseRule, smsOnly)
	got, snap, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if snap.SMS == 0 || snap.Email != 0 || !got.SendSMS || got.SendEmail {
		t.Fatalf("snapshot %+v, channels sms %v email %v", snap, got.SendSMS, got.SendEmail)
	}
	if _, email := deliveryCounts(t, pool, c.ID); email != 0 {
		t.Fatalf("%d email deliveries for an SMS-only campaign", email)
	}
}

func TestApprovePartial(t *testing.T) {
	pool := seededPool(t)
	svc, c := draft(t, pool, partialRule, goodMessage)

	_, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true})
	if got := approvalCodes(t, err); !slices.Equal(got, []string{CodePartialNotAcked}) {
		t.Fatalf("codes %v", got)
	}
	got, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true, Email: true, PartialAck: true})
	if err != nil {
		t.Fatalf("approve with ack: %v", err)
	}
	if got.PartialAckAt == nil || !got.PartialAckAt.Equal(testNow) {
		t.Fatalf("partial_ack_at %v", got.PartialAckAt)
	}
	stored, err := svc.Get(t.Context(), c.ID)
	if err != nil || stored.PartialAckAt == nil {
		t.Fatalf("stored partial_ack_at missing: %+v, %v", stored, err)
	}
}

// TestApproveRefusalsRollBack: a refused approval leaves a draft with no
// deliveries, even when the refusal comes after the snapshot insert.
func TestApproveRefusalsRollBack(t *testing.T) {
	pool := seededPool(t)
	// Fits with the case audience's names, but not with the longest name
	// in the seed (31 runes): 31 + 130 = 161.
	longSMS := goodMessage
	longSMS.SMS = "{{first_name}}" + strings.Repeat("a", content.MaxSMSRunes-len([]rune(seed.LongestFirstName))+1)

	tests := []struct {
		name string
		rule string
		msg  content.Message
		req  ApproveRequest
		want []string
	}{
		{"no channel", caseRule, goodMessage, ApproveRequest{}, []string{CodeNoChannel}},
		{"rejected rule", rejectedRule, goodMessage, ApproveRequest{SMS: true}, []string{CodeRuleNotApproved}},
		{"empty audience", emptyRule, goodMessage, ApproveRequest{SMS: true, Email: true}, []string{CodeEmptyAudience}},
		{"empty sms text", caseRule, content.Message{EmailSubject: "Konu", EmailBody: "Gövde"}, ApproveRequest{SMS: true, Email: true}, []string{content.CodeEmpty}},
		{"sms too long for this audience", istanbulRule, longSMS, ApproveRequest{SMS: true}, []string{content.CodeTooLong}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, c := draft(t, pool, tt.rule, tt.msg)
			_, _, err := svc.Approve(t.Context(), c.ID, tt.req)
			if got := approvalCodes(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("codes %v, want %v", got, tt.want)
			}
			after, err := svc.Get(t.Context(), c.ID)
			if err != nil || after.Status != StatusDraft || after.ApprovedAt != nil {
				t.Fatalf("after refusal: %+v, %v", after, err)
			}
			if sms, email := deliveryCounts(t, pool, c.ID); sms+email != 0 {
				t.Fatalf("refused approval left %d deliveries", sms+email)
			}
		})
	}

	// The same long SMS passes for the case audience: the worst case uses
	// the longest name of the audience, not of every user (D-CONTENT).
	svc, c := draft(t, pool, caseRule, longSMS)
	if _, _, err := svc.Approve(t.Context(), c.ID, ApproveRequest{SMS: true}); err != nil {
		t.Fatalf("long sms for the case audience: %v", err)
	}
}

// TestSaveContentUsesAudience: the worst-case stats of a saved message come
// from the draft's own audience.
func TestSaveContentUsesAudience(t *testing.T) {
	pool := seededPool(t)
	msg := content.Message{SMS: "{{first_name}}", EmailSubject: "Konu", EmailBody: "Gövde"}

	worst := func(rule string) int {
		svc, c := draft(t, pool, rule, content.Message{})
		_, res, err := svc.SaveContent(t.Context(), c.ID, msg)
		if err != nil {
			t.Fatal(err)
		}
		return res.Stats[content.FieldSMS].WorstCaseRunes
	}
	if got, want := worst(istanbulRule), len([]rune(seed.LongestFirstName)); got != want {
		t.Errorf("İstanbul audience: worst case %d, want %d (longest name cohort)", got, want)
	}
	if got := worst(caseRule); got >= len([]rune(seed.LongestFirstName)) {
		t.Errorf("case audience: worst case %d, should be shorter than the longest name", got)
	}
	// A rule without an audience falls back to every user.
	if got, want := worst(rejectedRule), len([]rune(seed.LongestFirstName)); got != want {
		t.Errorf("rejected rule: worst case %d, want %d", got, want)
	}
}
