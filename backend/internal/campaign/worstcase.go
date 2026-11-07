// Package campaign orchestrates the campaign flow and owns its Postgres
// queries. For now it only provides the values for the worst-case content
// check; the state machine and approval come in PLAN step 7.
package campaign

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
)

// LongestValues returns the longest first_name and city among all users,
// for the worst-case length check of message templates (D-CONTENT).
//
// It looks at every user, not only the campaign's audience. The audience is
// a subset, so this never under-estimates; it can be stricter than needed.
// The audience-specific version comes with campaign drafts (PLAN step 7).
//
// Postgres char_length counts characters, like utf8.RuneCountInString.
// Ties are broken by id so the result is deterministic.
func LongestValues(ctx context.Context, db *pgxpool.Pool) (content.Values, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	const q = `
SELECT
    coalesce((SELECT first_name FROM users ORDER BY char_length(first_name) DESC, id LIMIT 1), ''),
    coalesce((SELECT city FROM users ORDER BY char_length(city) DESC, id LIMIT 1), '')`
	var name, city string
	if err := db.QueryRow(ctx, q).Scan(&name, &city); err != nil {
		return nil, fmt.Errorf("query longest values: %w", err)
	}
	return content.Values{content.VarFirstName: name, content.VarCity: city}, nil
}
