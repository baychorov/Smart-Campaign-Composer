package campaign

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// ApproveRequest is what the marketer sees and confirms on the approval
// screen. It travels with the approval itself, so the stored choice is
// exactly the one on screen when the button was clicked.
type ApproveRequest struct {
	SMS, Email bool // channel checkboxes (D-CHANNEL)
	// PartialAck: "continue without the criteria that could not be
	// applied". Required when the rule's status is partial (D-PARTIAL).
	PartialAck bool
	// SendAt schedules the campaign; nil sends now. The audience is still
	// computed now, at approval (D-SNAPSHOT); only sending waits.
	SendAt *time.Time
}

// checkSendAt returns an issue when t is not a usable send time: it must be
// after now and at most MaxScheduleLead after now. How close to now it is
// is the marketer's choice; a past time would send at once while showing
// "scheduled", so it is refused.
func checkSendAt(t, now time.Time) *Issue {
	switch {
	case !t.After(now):
		return &Issue{Path: "send_at", Code: CodeInvalidSendAt,
			Message: "Lütfen ileri bir tarih/saat seçiniz."}
	case t.After(now.Add(MaxScheduleLead)):
		return &Issue{Path: "send_at", Code: CodeInvalidSendAt,
			Message: fmt.Sprintf("Gönderim zamanı en fazla %d gün sonrası olabilir.", int(MaxScheduleLead.Hours()/24))}
	}
	return nil
}

// Snapshot is the number of deliveries written per channel.
type Snapshot struct {
	SMS   int64 `json:"sms"`
	Email int64 `json:"email"`
}

// Approve moves a draft to approved and writes its audience snapshot to
// deliveries, all in one transaction (D-DEDUP layers 1 and 2, D-SNAPSHOT):
//
//  1. UPDATE ... WHERE status = 'draft' RETURNING the campaign. This locks
//     the row; a concurrent approval waits, then matches no row and gets
//     ErrNotDraft. The returned row is the content being approved: edits
//     also need status = 'draft', so none can slip in afterwards.
//  2. Checks: a channel, an approvable rule, the partial acknowledgement.
//  3. INSERT ... SELECT per channel from segment.CompileAudience, which
//     always includes the channel's consent filter (R-AUD-2). "Last N
//     days" windows are anchored at the approval time.
//  4. Content check with the longest values of the snapshot itself.
//
// With req.SendAt the campaign becomes scheduled instead of approved and
// its deliveries are due at SendAt (PLAN step 15a); everything else,
// including the snapshot, happens now.
//
// Any failed check rolls everything back and returns *ApprovalError.
func (s *Service) Approve(ctx context.Context, id int64, req ApproveRequest) (Campaign, Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, approveTimeout)
	defer cancel()
	now := s.now()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Campaign{}, Snapshot{}, fmt.Errorf("begin approval: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this undoes
	// everything on every early return.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// A scheduled campaign gets its snapshot now like any other; its
	// deliveries only become due at SendAt.
	status, due := StatusApproved, now
	if req.SendAt != nil {
		status, due = StatusScheduled, *req.SendAt
	}
	c, err := scanCampaign(tx.QueryRow(ctx, `
UPDATE campaigns
SET status = $2, approved_at = $3, updated_at = $3, send_sms = $4, send_email = $5, scheduled_at = $7
WHERE id = $1 AND status = $6
RETURNING `+columns, id, status, now, req.SMS, req.Email, StatusDraft, req.SendAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, Snapshot{}, notDraftReason(ctx, tx, id)
	}
	if err != nil {
		return Campaign{}, Snapshot{}, fmt.Errorf("approve campaign %d: %w", id, err)
	}

	v := segment.Validate(c.Rule)
	issues := precheck(v, req)
	if req.SendAt != nil {
		if is := checkSendAt(*req.SendAt, now); is != nil {
			issues = append(issues, *is)
		}
	}
	if len(issues) > 0 {
		return Campaign{}, Snapshot{}, &ApprovalError{Issues: issues}
	}
	if v.Rule.Status == segment.StatusPartial {
		if _, err := tx.Exec(ctx, `UPDATE campaigns SET partial_ack_at = $2 WHERE id = $1`, id, now); err != nil {
			return Campaign{}, Snapshot{}, fmt.Errorf("store partial acknowledgement: %w", err)
		}
		c.PartialAckAt = &now
	}

	var snap Snapshot
	if req.SMS {
		if snap.SMS, err = insertDeliveries(ctx, tx, id, v.Rule, segment.ChannelSMS, now, due); err != nil {
			return Campaign{}, Snapshot{}, err
		}
	}
	if req.Email {
		if snap.Email, err = insertDeliveries(ctx, tx, id, v.Rule, segment.ChannelEmail, now, due); err != nil {
			return Campaign{}, Snapshot{}, err
		}
	}

	issues = nil
	if snap.SMS+snap.Email == 0 {
		issues = append(issues, Issue{Code: CodeEmptyAudience, Message: "Seçilen kanallarda izni olan hiç kullanıcı yok."})
	}
	longest, err := snapshotLongest(ctx, tx, id)
	if err != nil {
		return Campaign{}, Snapshot{}, err
	}
	issues = append(issues, contentIssues(content.Validate(c.Message, longest), req)...)
	if len(issues) > 0 {
		return Campaign{}, Snapshot{}, &ApprovalError{Issues: issues}
	}

	if err := tx.Commit(ctx); err != nil {
		return Campaign{}, Snapshot{}, fmt.Errorf("commit approval: %w", err)
	}
	return c, snap, nil
}

// insertDeliveries writes one pending delivery per audience member of the
// channel and returns how many were written. The audience is computed at
// now; the rows become due at due (now, or the scheduled send time). A
// duplicate would violate UNIQUE(campaign_id, user_id, channel) and fail
// the whole approval.
func insertDeliveries(ctx context.Context, tx pgx.Tx, campaignID int64, rule segment.Rule, ch segment.Channel, now, due time.Time) (int64, error) {
	q, err := segment.CompileAudience(rule, ch, now)
	if err != nil {
		return 0, fmt.Errorf("compile %s audience: %w", ch, err)
	}
	n := len(q.Args)
	sql := `INSERT INTO deliveries (campaign_id, user_id, channel, next_attempt_at)
SELECT $` + strconv.Itoa(n+1) + `, a.id, $` + strconv.Itoa(n+2) + `, $` + strconv.Itoa(n+3) + ` FROM (` + q.SQL + `) a`
	tag, err := tx.Exec(ctx, sql, append(q.Args, campaignID, string(ch), due)...)
	if err != nil {
		return 0, fmt.Errorf("insert %s deliveries: %w", ch, err)
	}
	return tag.RowsAffected(), nil
}

// precheck returns the approval issues that need no snapshot.
func precheck(v segment.Result, req ApproveRequest) []Issue {
	var issues []Issue
	if !req.SMS && !req.Email {
		issues = append(issues, Issue{Path: "channels", Code: CodeNoChannel, Message: "En az bir kanal seçilmeli."})
	}
	switch {
	case !v.Approvable():
		issues = append(issues, Issue{Path: "rule", Code: CodeRuleNotApproved,
			Message: fmt.Sprintf("Kural bir kitle tanımlamıyor (durum: %s, %d hata).", v.Rule.Status, len(v.Errors))})
	case v.Rule.Status == segment.StatusPartial && !req.PartialAck:
		issues = append(issues, Issue{Path: "partial_ack", Code: CodePartialNotAcked,
			Message: "Kural kısmi: uygulanamayan kriterler olmadan devam etmek açıkça onaylanmalı."})
	}
	return issues
}

// contentIssues keeps the content errors of the selected channels: SMS
// text for SMS, subject and body for email.
func contentIssues(res content.Result, req ApproveRequest) []Issue {
	var issues []Issue
	for _, e := range res.Errors {
		sms := e.Path == content.FieldSMS
		if (sms && req.SMS) || (!sms && req.Email) {
			issues = append(issues, Issue{Path: "message." + e.Path, Code: e.Code, Message: e.Message})
		}
	}
	return issues
}
