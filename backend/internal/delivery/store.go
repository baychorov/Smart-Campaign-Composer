package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres queue.
type PGStore struct {
	db *pgxpool.Pool
}

// NewPGStore returns a Store on db.
func NewPGStore(db *pgxpool.Pool) *PGStore { return &PGStore{db: db} }

// claimSQL picks due rows and leases them in one statement.
//
// A row is due when it is pending and its next_attempt_at has passed, or
// when it is "sending" but its lease expired: the worker that claimed it
// died (D-DEDUP layer 3). FOR UPDATE SKIP LOCKED lets several claimers run
// at once without waiting for, or taking, each other's rows.
//
// The claim counts as an attempt. The recipient and the campaign copy are
// read in the same statement, so consent is as fresh as the claim.
const claimSQL = `
WITH due AS (
    SELECT id FROM deliveries
    WHERE channel = $1
      AND ((status = 'pending' AND next_attempt_at <= $2)
        OR (status = 'sending' AND locked_until <= $2))
    ORDER BY next_attempt_at, id
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
UPDATE deliveries d
SET status = 'sending', locked_until = $4, attempts = d.attempts + 1, updated_at = $2
FROM due, users u, campaigns c
WHERE d.id = due.id AND u.id = d.user_id AND c.id = d.campaign_id
RETURNING d.id, d.campaign_id, d.channel, d.attempts, d.throttles,
    CASE d.channel WHEN 'sms' THEN u.sms_opt_in ELSE u.email_opt_in END,
    CASE d.channel WHEN 'sms' THEN u.phone ELSE u.email END,
    u.first_name, u.city,
    coalesce(c.sms_text, ''), coalesce(c.email_subject, ''), coalesce(c.email_body, '')`

// Claim implements Store. Campaigns whose first rows are claimed move from
// approved (or scheduled) to sending in the same transaction. A scheduled
// campaign's rows are not due before its send time (next_attempt_at), so
// the claim query needs no other change for scheduling.
func (s *PGStore) Claim(ctx context.Context, ch Channel, limit int, now, leaseUntil time.Time) ([]Job, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, claimSQL, string(ch), now, limit, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("claim %s deliveries: %w", ch, err)
	}
	jobs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		var j Job
		var channel string
		err := r.Scan(&j.ID, &j.CampaignID, &channel, &j.Attempts, &j.Throttles,
			&j.Consent, &j.To, &j.FirstName, &j.City, &j.SMSText, &j.EmailSubject, &j.EmailBody)
		j.Channel = Channel(channel)
		return j, err
	})
	if err != nil {
		return nil, fmt.Errorf("read claimed %s deliveries: %w", ch, err)
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.CampaignID)
	}
	if _, err := tx.Exec(ctx, `
UPDATE campaigns SET status = 'sending', updated_at = $2
WHERE id = ANY($1) AND status IN ('approved', 'scheduled')`, ids, now); err != nil {
		return nil, fmt.Errorf("mark campaigns sending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return jobs, nil
}

// Complete implements Store. The WHERE clause is the fence: the row must
// still be "sending" with this claim's attempt count. If the lease expired
// and another worker re-claimed the row, attempts has moved on and this
// stale result is dropped.
func (s *PGStore) Complete(ctx context.Context, job Job, d Decision, now time.Time) (bool, error) {
	var set string
	args := []any{job.ID, job.Attempts, now}
	switch d.Kind {
	case KindSent:
		set = `status = 'sent', sent_at = $3, last_error = NULL`
	case KindFailed, KindSkipped:
		set = `status = $4, last_error = $5`
		args = append(args, string(d.Kind), d.Error)
	case KindRetry:
		set = `status = 'pending', next_attempt_at = $4, last_error = $5`
		args = append(args, now.Add(d.Delay), d.Error)
		if d.Throttled {
			// A 429 does not use up an attempt; it has its own counter.
			set += `, attempts = attempts - 1, throttles = throttles + 1`
		}
	case KindRelease:
		set = `status = 'pending', next_attempt_at = $3, attempts = attempts - 1`
	default:
		return false, fmt.Errorf("complete delivery %d: unknown decision %q", job.ID, d.Kind)
	}

	tag, err := s.db.Exec(ctx, `UPDATE deliveries SET `+set+`, locked_until = NULL, updated_at = $3
WHERE id = $1 AND status = 'sending' AND attempts = $2`, args...)
	if err != nil {
		return false, fmt.Errorf("complete delivery %d: %w", job.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// FinishCampaigns implements Store.
func (s *PGStore) FinishCampaigns(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `
UPDATE campaigns c SET status = 'completed', updated_at = $1
WHERE c.status = 'sending'
  AND NOT EXISTS (SELECT 1 FROM deliveries d
                  WHERE d.campaign_id = c.id AND d.status IN ('pending', 'sending'))`, now)
	if err != nil {
		return 0, fmt.Errorf("finish campaigns: %w", err)
	}
	return tag.RowsAffected(), nil
}
