package campaign

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RetryFailed puts a completed campaign's failed deliveries back in the
// queue: the marketer's manual "send the failed ones again" after the
// automatic retries (D-RETRY) were used up. It returns the campaign, now
// sending, and the number of requeued rows.
//
// The rows are reused, not copied: same id, so the same Idempotency-Key
// (D-DEDUP). A row that failed after the provider had in fact delivered it
// (accepted, then 500) gets the stored success back on the retry, not a
// second message (R-SEND-3). Sent and skipped rows are not touched, and
// consent is read again when the worker claims the row.
//
// Only a completed campaign can be retried: while it is sending, the
// worker still owns its rows. The status change is
// UPDATE ... WHERE status = 'completed', so of two concurrent retries only
// one requeues the rows; the other gets ErrNotCompleted.
func (s *Service) RetryFailed(ctx context.Context, id int64) (Campaign, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Campaign{}, 0, fmt.Errorf("begin retry: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	now := s.now()
	c, err := scanCampaign(tx.QueryRow(ctx, `
UPDATE campaigns SET status = $2, updated_at = $3
WHERE id = $1 AND status = $4
RETURNING `+columns, id, StatusSending, now, StatusCompleted))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, 0, notCompletedReason(ctx, tx, id)
	}
	if err != nil {
		return Campaign{}, 0, fmt.Errorf("reopen campaign %d: %w", id, err)
	}

	// Fresh attempt and 429 budgets; last_error is cleared so the
	// dashboard's error list shows only what happens from now on.
	tag, err := tx.Exec(ctx, `
UPDATE deliveries
SET status = 'pending', attempts = 0, throttles = 0, next_attempt_at = $2,
    locked_until = NULL, last_error = NULL, updated_at = $2
WHERE campaign_id = $1 AND status = 'failed'`, id, now)
	if err != nil {
		return Campaign{}, 0, fmt.Errorf("requeue failed deliveries of campaign %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Rolled back: the campaign stays completed.
		return Campaign{}, 0, ErrNothingToRetry
	}
	if err := tx.Commit(ctx); err != nil {
		return Campaign{}, 0, fmt.Errorf("commit retry: %w", err)
	}
	return c, tag.RowsAffected(), nil
}

// notCompletedReason explains why "WHERE status = 'completed'" matched no
// row. Inside a transaction, q must be that transaction (see
// notDraftReason).
func notCompletedReason(ctx context.Context, q querier, id int64) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check campaign %d: %w", id, err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotCompleted
}
