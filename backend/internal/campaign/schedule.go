package campaign

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgLockNotAvailable is Postgres's error for FOR UPDATE NOWAIT on a row
// another transaction holds.
const pgLockNotAvailable = "55P03"

// Cancel stops a scheduled campaign before it is sent: the campaign becomes
// cancelled and its pending deliveries are deleted, in one transaction.
//
// The worker may claim the campaign's first rows at the same moment. The
// rule is "whoever locks the delivery rows first wins", and neither side
// waits for the other, so the two cannot deadlock:
//
//   - The worker claims due rows with FOR UPDATE SKIP LOCKED. If Cancel
//     holds them, the worker skips them; Cancel then deletes them.
//   - Cancel locks the rows with FOR UPDATE NOWAIT. If the worker holds
//     even one, NOWAIT fails at once, Cancel rolls back and returns
//     ErrSendingStarted: sending has begun and is not interrupted.
//
// A campaign the worker has already moved to sending is not scheduled any
// more, so Cancel returns ErrNotScheduled.
func (s *Service) Cancel(ctx context.Context, id int64) (Campaign, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Campaign{}, fmt.Errorf("begin cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := scanCampaign(tx.QueryRow(ctx, `
UPDATE campaigns SET status = $2, updated_at = $3
WHERE id = $1 AND status = $4
RETURNING `+columns, id, StatusCancelled, s.now(), StatusScheduled))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, s.notScheduledReason(ctx, id)
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("cancel campaign %d: %w", id, err)
	}

	if _, err := tx.Exec(ctx, `SELECT id FROM deliveries WHERE campaign_id = $1 FOR UPDATE NOWAIT`, id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgLockNotAvailable {
			return Campaign{}, ErrSendingStarted
		}
		return Campaign{}, fmt.Errorf("lock deliveries of campaign %d: %w", id, err)
	}
	// Every row is still pending: the worker moves a campaign to sending in
	// the same transaction in which it claims its first row.
	if _, err := tx.Exec(ctx, `DELETE FROM deliveries WHERE campaign_id = $1`, id); err != nil {
		return Campaign{}, fmt.Errorf("delete deliveries of campaign %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Campaign{}, fmt.Errorf("commit cancel: %w", err)
	}
	return c, nil
}

// notScheduledReason explains why "WHERE status = 'scheduled'" matched no
// row: the campaign is missing, or it is in another state.
func (s *Service) notScheduledReason(ctx context.Context, id int64) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check campaign %d: %w", id, err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotScheduled
}
