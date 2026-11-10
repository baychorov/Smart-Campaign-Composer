package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Timeouts for outside calls.
const (
	extractTimeout = 2 * time.Minute // segment extraction, repairs included
	dbTimeout      = 10 * time.Second
	approveTimeout = 60 * time.Second // the snapshot insert can be large
)

// ErrNotApprovable: the stored rule does not describe an audience (status
// unsupported or rejected), so content cannot be generated for it.
var ErrNotApprovable = errors.New("campaign rule does not describe an audience")

// Service runs the campaign flow against Postgres and the LLM.
type Service struct {
	db  *pgxpool.Pool
	llm llm.Client
	now func() time.Time
}

// NewService builds a Service. now anchors the "last N days" windows of
// counts and of the approval snapshot; nil means time.Now.
func NewService(db *pgxpool.Pool, client llm.Client, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, llm: client, now: now}
}

// querier is what both *pgxpool.Pool and pgx.Tx offer, so helpers run
// inside or outside a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// columns is the select list read by scanCampaign. Nullable text columns
// are coalesced so they scan into plain strings.
const columns = `id, brief, status, rule,
    coalesce(sms_text, ''), coalesce(email_subject, ''), coalesce(email_body, ''),
    send_sms, send_email, partial_ack_at, approved_at, created_at, updated_at, scheduled_at`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	var rule []byte
	err := row.Scan(&c.ID, &c.Brief, &c.Status, &rule,
		&c.Message.SMS, &c.Message.EmailSubject, &c.Message.EmailBody,
		&c.SendSMS, &c.SendEmail, &c.PartialAckAt, &c.ApprovedAt, &c.CreatedAt, &c.UpdatedAt, &c.ScheduledAt)
	if err != nil {
		return Campaign{}, err
	}
	if err := json.Unmarshal(rule, &c.Rule); err != nil {
		return Campaign{}, fmt.Errorf("decode stored rule of campaign %d: %w", c.ID, err)
	}
	return c, nil
}

// Create extracts a rule from the brief and stores it as a new draft. The
// draft is stored even when the rule's status is unsupported or rejected,
// so the marketer sees why; such a draft cannot be approved.
//
// The returned SegmentResult carries usage for logging and, with
// llm.ErrInvalidOutput, the last validation issues. No campaign is stored
// when err is not nil.
func (s *Service) Create(ctx context.Context, brief string) (Campaign, llm.SegmentResult, error) {
	brief, err := llm.CheckBrief(brief)
	if err != nil {
		return Campaign{}, llm.SegmentResult{}, err
	}
	llmCtx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	res, err := llm.ExtractSegment(llmCtx, s.llm, brief, s.now())
	if err != nil {
		return Campaign{}, res, err
	}

	rule, err := json.Marshal(res.Validation.Rule)
	if err != nil {
		return Campaign{}, res, fmt.Errorf("encode rule: %w", err)
	}
	// The send time read from the brief is kept as a suggestion for the
	// approval screen. One the approval would refuse is dropped here, with a
	// warning, so the marketer is not shown a time that cannot be used.
	sendAt := res.SendAt
	if sendAt != nil {
		if is := checkSendAt(*sendAt, s.now()); is != nil {
			res.Validation.Warnings = append(res.Validation.Warnings,
				segment.Issue{Path: "send_at", Code: is.Code, Message: "Brief'teki gönderim zamanı kullanılamadı: " + is.Message})
			sendAt = nil
		}
	}

	ctx, cancel = context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	// Both channels start selected (D-CHANNEL); approval sets the final choice.
	row := s.db.QueryRow(ctx, `
INSERT INTO campaigns (brief, status, rule, send_sms, send_email, created_at, updated_at, scheduled_at)
VALUES ($1, $2, $3, true, true, $4, $4, $5)
RETURNING `+columns, brief, StatusDraft, rule, s.now(), sendAt)
	c, err := scanCampaign(row)
	if err != nil {
		return Campaign{}, res, fmt.Errorf("insert campaign: %w", err)
	}
	return c, res, nil
}

// Get returns a campaign or ErrNotFound.
func (s *Service) Get(ctx context.Context, id int64) (Campaign, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	c, err := scanCampaign(s.db.QueryRow(ctx, `SELECT `+columns+` FROM campaigns WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("get campaign %d: %w", id, err)
	}
	return c, nil
}

// UpdateRule replaces the rule of a draft with a marketer-edited one. The
// rule is validated and stored normalised; with validation errors nothing
// is stored and a *RuleError is returned. The result carries the warnings.
//
// An edit changes the conditions only (D-FE: remove conditions or values,
// change numbers). The model's verdict stays: the status, the audience
// scope and the criteria that could not be applied must be the stored
// ones (CodeRuleLocked otherwise). Without this, an API client could turn
// a partial rule into ok and skip the acknowledgement (D-PARTIAL), or give
// a rejected brief an audience. A rule that is not approvable cannot be
// edited at all (ErrNotApprovable): it has no conditions to edit.
func (s *Service) UpdateRule(ctx context.Context, id int64, rule segment.Rule) (Campaign, segment.Result, error) {
	v := segment.Validate(rule)
	if !v.Valid() {
		return Campaign{}, v, &RuleError{Issues: v.Errors}
	}
	b, err := json.Marshal(v.Rule)
	if err != nil {
		return Campaign{}, v, fmt.Errorf("encode rule: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	stored, err := s.Get(ctx, id)
	if err != nil {
		return Campaign{}, v, err
	}
	if stored.Status != StatusDraft {
		return Campaign{}, v, ErrNotDraft
	}
	sv := segment.Validate(stored.Rule)
	if !sv.Approvable() {
		return Campaign{}, v, ErrNotApprovable
	}
	if issues := lockedChanges(sv.Rule, v.Rule); len(issues) > 0 {
		return Campaign{}, v, &RuleError{Issues: issues}
	}
	// Two edits at the same time both started from a rule with these
	// locked parts, so whichever is stored last keeps them too.
	c, err := s.updateDraft(ctx, id, `rule = $3`, b)
	return c, v, err
}

// CodeRuleLocked: a rule edit tried to change the model's verdict (status,
// audience scope or the criteria that could not be applied).
const CodeRuleLocked = "rule_locked"

// lockedChanges lists the parts of the model's verdict that next changes
// compared with stored. Both rules are normalised. The group index of an
// unsupported criterion may change: removing a group renumbers the others.
func lockedChanges(stored, next segment.Rule) []segment.Issue {
	var issues []segment.Issue
	locked := func(path, msg string) {
		issues = append(issues, segment.Issue{Path: path, Code: CodeRuleLocked, Message: msg})
	}
	if next.Status != stored.Status {
		locked("status", "Kuralın durumu düzenlemeyle değiştirilemez.")
	}
	if next.AudienceScope != stored.AudienceScope {
		locked("audience_scope", "Kitle kapsamı düzenlemeyle değiştirilemez.")
	}
	same := len(next.Unsupported) == len(stored.Unsupported)
	for i := 0; same && i < len(next.Unsupported); i++ {
		a, b := next.Unsupported[i], stored.Unsupported[i]
		same = a.Text == b.Text && a.Reason == b.Reason
	}
	if !same {
		locked("unsupported", "Uygulanmayan kriterler düzenlemeyle değiştirilemez.")
	}
	return issues
}

// SaveContent stores the draft's message and returns its validation, made
// with the longest values of the rule's audience (D-CONTENT). An invalid
// message is stored too, as work in progress; approval checks it again.
func (s *Service) SaveContent(ctx context.Context, id int64, m content.Message) (Campaign, content.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	c, err := s.Get(ctx, id)
	if err != nil {
		return Campaign{}, content.Result{}, err
	}
	if c.Status != StatusDraft {
		return Campaign{}, content.Result{}, ErrNotDraft
	}
	longest, err := s.audienceLongest(ctx, c.Rule)
	if err != nil {
		return Campaign{}, content.Result{}, err
	}
	res := content.Validate(m, longest)
	c, err = s.updateDraft(ctx, id, `sms_text = $3, email_subject = $4, email_body = $5`,
		res.Message.SMS, res.Message.EmailSubject, res.Message.EmailBody)
	return c, res, err
}

// ContentValidation validates the stored message of a draft against the
// longest values of its current audience, the same check SaveContent makes.
// The rule may have changed since the message was saved, so the UI reads
// this instead of keeping an old result. ok is false for a non-draft or a
// draft without any message text yet.
func (s *Service) ContentValidation(ctx context.Context, c Campaign) (res content.Result, ok bool, err error) {
	if c.Status != StatusDraft || c.Message == (content.Message{}) {
		return content.Result{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	longest, err := s.audienceLongest(ctx, c.Rule)
	if err != nil {
		return content.Result{}, false, err
	}
	return content.Validate(c.Message, longest), true, nil
}

// GenerationInput returns what content generation needs for a draft: the
// model input and the longest values of its audience.
func (s *Service) GenerationInput(ctx context.Context, id int64) (llm.ContentInput, content.Values, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	c, err := s.Get(ctx, id)
	if err != nil {
		return llm.ContentInput{}, nil, err
	}
	if c.Status != StatusDraft {
		return llm.ContentInput{}, nil, ErrNotDraft
	}
	if !segment.Validate(c.Rule).Approvable() {
		return llm.ContentInput{}, nil, ErrNotApprovable
	}
	longest, err := s.audienceLongest(ctx, c.Rule)
	if err != nil {
		return llm.ContentInput{}, nil, err
	}
	return ContentInput(c), longest, nil
}

// ContentInput is the model input for content generation: the brief, a
// summary generated from the rule (not the raw rule) and the rule's notes.
func ContentInput(c Campaign) llm.ContentInput {
	return llm.ContentInput{
		Brief:    c.Brief,
		Audience: segment.Summarize(c.Rule).String(),
		Notes:    c.Rule.Notes,
	}
}

// updateDraft runs UPDATE campaigns SET <set> WHERE id = $1 AND status =
// 'draft'. set uses $3 and up for args; $2 is the update time.
func (s *Service) updateDraft(ctx context.Context, id int64, set string, args ...any) (Campaign, error) {
	all := append([]any{id, s.now()}, args...)
	row := s.db.QueryRow(ctx, `UPDATE campaigns SET `+set+`, updated_at = $2
WHERE id = $1 AND status = '`+string(StatusDraft)+`'
RETURNING `+columns, all...)
	c, err := scanCampaign(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, notDraftReason(ctx, s.db, id)
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("update campaign %d: %w", id, err)
	}
	return c, nil
}

// notDraftReason explains why a "WHERE status = 'draft'" update matched no
// row: the campaign is missing, or it has left the draft state.
//
// Inside a transaction, q must be that transaction: asking the pool for a
// second connection while holding one deadlocks the pool when many callers
// do it at once (TestApproveConcurrent).
func notDraftReason(ctx context.Context, q querier, id int64) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check campaign %d: %w", id, err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotDraft
}

// Audience is the size of a campaign's audience. For a draft it is
// computed live from the rule; after approval it is read from the
// snapshot in deliveries (Snapshot true, no per-group counts).
type Audience struct {
	Snapshot  bool    `json:"snapshot"`
	Reachable int64   `json:"reachable"` // distinct users on any channel
	SMS       int64   `json:"sms"`
	Email     int64   `json:"email"`
	Groups    []int64 `json:"groups,omitempty"`
}

// Audience returns the audience counts of c. ok is false for a draft whose
// rule does not describe an audience.
func (s *Service) Audience(ctx context.Context, c Campaign) (a Audience, ok bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	if c.Status != StatusDraft {
		a.Snapshot = true
		err := s.db.QueryRow(ctx, `
SELECT count(DISTINCT user_id),
       count(*) FILTER (WHERE channel = $2),
       count(*) FILTER (WHERE channel = $3)
FROM deliveries WHERE campaign_id = $1`, c.ID, segment.ChannelSMS, segment.ChannelEmail).
			Scan(&a.Reachable, &a.SMS, &a.Email)
		if err != nil {
			return Audience{}, false, fmt.Errorf("count snapshot of campaign %d: %w", c.ID, err)
		}
		return a, true, nil
	}

	q, err := segment.CompileCounts(c.Rule, s.now())
	if errors.Is(err, segment.ErrNotCompilable) {
		return Audience{}, false, nil
	}
	if err != nil {
		return Audience{}, false, err
	}
	counts, err := q.Scan(s.db.QueryRow(ctx, q.SQL, q.Args...).Scan)
	if err != nil {
		return Audience{}, false, fmt.Errorf("count audience of campaign %d: %w", c.ID, err)
	}
	return Audience{Reachable: counts.Reachable, SMS: counts.SMS, Email: counts.Email, Groups: counts.Groups}, true, nil
}
