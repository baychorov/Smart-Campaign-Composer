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
// See unschedule for the race with the worker.
func (s *Service) Cancel(ctx context.Context, id int64) (Campaign, error) {
	return s.unschedule(ctx, id, StatusCancelled)
}

// Reopen turns a scheduled campaign back into a draft so the marketer can
// change it (rule, messages, channels, send time) and approve it again. Its
// pending deliveries are deleted and the approval is undone (approved_at,
// partial_ack_at), in one transaction. The send time stays as the draft's
// suggestion; the channels stay as they were chosen. A new approval takes
// a new snapshot (D-SNAPSHOT). See unschedule for the race with the worker.
func (s *Service) Reopen(ctx context.Context, id int64) (Campaign, error) {
	return s.unschedule(ctx, id, StatusDraft)
}

// unschedule moves a scheduled campaign to cancelled or back to draft and
// deletes its deliveries, in one transaction.
//
// The worker may claim the campaign's first rows at the same moment. The
// rule is "whoever locks the delivery rows first wins", and neither side
// waits for the other, so the two cannot deadlock:
//
//   - The worker claims due rows with FOR UPDATE SKIP LOCKED. If unschedule
//     holds them, the worker skips them; unschedule then deletes them.
//   - unschedule locks the rows with FOR UPDATE NOWAIT. If the worker holds
//     even one, NOWAIT fails at once, unschedule rolls back and returns
//     ErrSendingStarted: sending has begun and is not interrupted.
//
// A campaign the worker has already moved to sending is not scheduled any
// more, so unschedule returns ErrNotScheduled.
func (s *Service) unschedule(ctx context.Context, id int64, to Status) (Campaign, error) {
	if !CanTransition(StatusScheduled, to) || to == StatusSending {
		return Campaign{}, fmt.Errorf("unschedule campaign %d: bad target status %s", id, to)
	}
	// Back to draft also undoes the approval. Fixed SQL text only.
	set := "status = $2, updated_at = $3"
	if to == StatusDraft {
		set += ", approved_at = NULL, partial_ack_at = NULL"
	}

	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Campaign{}, fmt.Errorf("begin unschedule: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := scanCampaign(tx.QueryRow(ctx, `
UPDATE campaigns SET `+set+`
WHERE id = $1 AND status = $4
RETURNING `+columns, id, to, s.now(), StatusScheduled))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, notScheduledReason(ctx, tx, id)
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("unschedule campaign %d: %w", id, err)
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
		return Campaign{}, fmt.Errorf("commit unschedule: %w", err)
	}
	return c, nil
}

// notScheduledReason explains why "WHERE status = 'scheduled'" matched no
// row: the campaign is missing, or it is in another state. Inside a
// transaction, q must be that transaction (see notDraftReason).
func notScheduledReason(ctx context.Context, q querier, id int64) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check campaign %d: %w", id, err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotScheduled
}
