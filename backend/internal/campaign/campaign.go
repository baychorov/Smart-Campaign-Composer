// Package campaign owns the campaign lifecycle: a draft is created from a
// brief, edited, and approved by the marketer. Approval writes the audience
// snapshot to the deliveries queue in the same transaction (D-SNAPSHOT,
// D-DEDUP). It also owns the campaign's Postgres queries.
package campaign

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Status is the lifecycle state of a campaign. The values match the CHECK
// constraint on campaigns.status.
type Status string

const (
	StatusDraft     Status = "draft"     // editable; no deliveries yet
	StatusApproved  Status = "approved"  // snapshot written, waiting for the worker
	StatusScheduled Status = "scheduled" // snapshot written, held until ScheduledAt
	StatusSending   Status = "sending"   // the worker has started (PLAN step 9)
	StatusCompleted Status = "completed" // every delivery is final
	StatusCancelled Status = "cancelled" // a scheduled campaign stopped before sending
)

// MaxScheduleLead is the latest send time of a scheduled campaign, counted
// from now. The earliest is any moment after now (PLAN step 15a).
const MaxScheduleLead = 30 * 24 * time.Hour

// transitions is the state machine. Every change of campaigns.status must
// be one of these edges, and is written as
// UPDATE ... WHERE status = <from>, so of two concurrent requests only one
// can make the move.
var transitions = map[Status][]Status{
	StatusDraft:    {StatusApproved, StatusScheduled},
	StatusApproved: {StatusSending},
	// Back to draft: the marketer edits a scheduled campaign (Reopen).
	StatusScheduled: {StatusSending, StatusCancelled, StatusDraft},
	StatusSending:   {StatusCompleted},
	// Manual retry of failed messages (RetryFailed).
	StatusCompleted: {StatusSending},
}

// CanTransition reports whether the state machine allows from → to.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Campaign is one row of the campaigns table.
type Campaign struct {
	ID      int64
	Brief   string
	Status  Status
	Rule    segment.Rule // normalised (the output of segment.Validate)
	Message content.Message
	// Channels chosen at approval (D-CHANNEL). Meaningless for a draft.
	SendSMS, SendEmail bool
	// PartialAckAt is when the marketer approved a partial rule without the
	// criteria it could not express (D-PARTIAL).
	PartialAckAt *time.Time
	ApprovedAt   *time.Time
	// ScheduledAt is the send time: for a draft the model's suggestion from
	// the brief, after approval the time the marketer chose. Nil means now.
	ScheduledAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

var (
	// ErrNotFound: no campaign with that id.
	ErrNotFound = errors.New("campaign not found")
	// ErrNotDraft: the campaign is no longer a draft, so it cannot be edited
	// or approved again.
	ErrNotDraft = errors.New("campaign is not a draft")
	// ErrNotScheduled: only a scheduled campaign can be cancelled or
	// reopened for editing.
	ErrNotScheduled = errors.New("campaign is not scheduled")
	// ErrSendingStarted: the worker took the campaign's first messages
	// while the cancellation (or reopening) was running; sending wins.
	ErrSendingStarted = errors.New("campaign sending has started")
	// ErrNotCompleted: failed messages can be retried only after the
	// campaign has completed.
	ErrNotCompleted = errors.New("campaign is not completed")
	// ErrNothingToRetry: the completed campaign has no failed messages.
	ErrNothingToRetry = errors.New("campaign has no failed messages")
)

// Issue is one reason a request was refused. Message is Turkish: it is
// shown to the marketer.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Approval issue codes.
const (
	CodeNoChannel       = "no_channel"
	CodeRuleNotApproved = "rule_not_approvable"
	CodePartialNotAcked = "partial_not_acknowledged"
	CodeEmptyAudience   = "empty_audience"
	CodeInvalidSendAt   = "invalid_send_at"
	// Content errors keep the code from package content (too_long, ...).
)

// ApprovalError lists why an approval was refused. Nothing was written.
type ApprovalError struct {
	Issues []Issue
}

func (e *ApprovalError) Error() string {
	return fmt.Sprintf("approval refused: %d issue(s), first: %s", len(e.Issues), e.Issues[0].Code)
}

// RuleError is returned when a rule edit has validation errors. The stored
// rule is unchanged.
type RuleError struct {
	Issues []segment.Issue
}

func (e *RuleError) Error() string {
	return fmt.Sprintf("invalid rule: %d error(s)", len(e.Issues))
}
