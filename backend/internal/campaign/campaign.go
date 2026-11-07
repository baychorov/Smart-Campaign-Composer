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
	StatusSending   Status = "sending"   // the worker has started (PLAN step 9)
	StatusCompleted Status = "completed" // every delivery is final
)

// transitions is the state machine. Every change of campaigns.status must
// be one of these edges, and is written as
// UPDATE ... WHERE status = <from>, so of two concurrent requests only one
// can make the move.
var transitions = map[Status][]Status{
	StatusDraft:    {StatusApproved},
	StatusApproved: {StatusSending},
	StatusSending:  {StatusCompleted},
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
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var (
	// ErrNotFound: no campaign with that id.
	ErrNotFound = errors.New("campaign not found")
	// ErrNotDraft: the campaign is no longer a draft, so it cannot be edited
	// or approved again.
	ErrNotDraft = errors.New("campaign is not a draft")
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
