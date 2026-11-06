package seed

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IsSeeded reports whether the users table already has rows.
func IsSeeded(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check seeded: %w", err)
	}
	return exists, nil
}

// Insert writes the dataset in one transaction using COPY, which streams
// all rows in a single round trip instead of one INSERT per row.
func Insert(ctx context.Context, pool *pgxpool.Pool, ds Dataset) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"users"},
		[]string{"id", "first_name", "last_name", "email", "phone", "city", "age", "sms_opt_in", "email_opt_in", "created_at"},
		pgx.CopyFromSlice(len(ds.Users), func(i int) ([]any, error) {
			u := ds.Users[i]
			return []any{u.ID, u.FirstName, u.LastName, u.Email, u.Phone, u.City, u.Age, u.SMSOptIn, u.EmailOptIn, u.CreatedAt}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy users: %w", err)
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"user_events"},
		[]string{"user_id", "event_type", "occurred_at"},
		pgx.CopyFromSlice(len(ds.Events), func(i int) ([]any, error) {
			e := ds.Events[i]
			return []any{e.UserID, e.Type, e.OccurredAt}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy events: %w", err)
	}

	// IDs were set explicitly, so move the identity sequence past them;
	// otherwise the next INSERT without an id would collide.
	if _, err := tx.Exec(ctx, "SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT max(id) FROM users))"); err != nil {
		return fmt.Errorf("advance users id sequence: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed tx: %w", err)
	}
	return nil
}
