package campaign

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Longest values of the personalisation variables, for the worst-case
// length check of message templates (D-CONTENT). Three scopes:
//   - LongestValues: every user (fallback when there is no audience)
//   - audienceLongest: users a draft's rule reaches on any channel
//   - snapshotLongest: users in an approval snapshot
//
// Postgres char_length counts characters, like utf8.RuneCountInString.
// Ties are broken by id so the result is deterministic.

// longestSQL picks the longest first_name and city among the user ids that
// idsSQL selects (one column named id). idsSQL is built from constants or
// by package segment, never from input text.
func longestSQL(idsSQL string) string {
	return `WITH a AS (` + idsSQL + `)
SELECT
    coalesce((SELECT u.first_name FROM users u JOIN a ON a.id = u.id
              ORDER BY char_length(u.first_name) DESC, u.id LIMIT 1), ''),
    coalesce((SELECT u.city FROM users u JOIN a ON a.id = u.id
              ORDER BY char_length(u.city) DESC, u.id LIMIT 1), '')`
}

func longest(ctx context.Context, q querier, idsSQL string, args ...any) (content.Values, error) {
	var name, city string
	if err := q.QueryRow(ctx, longestSQL(idsSQL), args...).Scan(&name, &city); err != nil {
		return nil, fmt.Errorf("query longest values: %w", err)
	}
	return content.Values{content.VarFirstName: name, content.VarCity: city}, nil
}

// LongestValues returns the longest first_name and city among all users.
// Every audience is a subset, so this never under-estimates.
func LongestValues(ctx context.Context, q querier) (content.Values, error) {
	return longest(ctx, q, `SELECT id FROM users`)
}

// audienceLongest returns the longest values among the users the rule
// reaches on SMS or email. Channels are chosen only at approval, so both
// count. A rule without an audience falls back to all users.
func (s *Service) audienceLongest(ctx context.Context, rule segment.Rule) (content.Values, error) {
	now := s.now()
	out := content.Values{}
	for _, ch := range []segment.Channel{segment.ChannelSMS, segment.ChannelEmail} {
		q, err := segment.CompileAudience(rule, ch, now)
		if errors.Is(err, segment.ErrNotCompilable) {
			return LongestValues(ctx, s.db)
		}
		if err != nil {
			return nil, err
		}
		vals, err := longest(ctx, s.db, q.SQL, q.Args...)
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			if utf8.RuneCountInString(v) > utf8.RuneCountInString(out[k]) {
				out[k] = v
			}
		}
	}
	return out, nil
}

// snapshotLongest returns the longest values among the users of a
// campaign's deliveries: exactly who will receive the message.
func snapshotLongest(ctx context.Context, q querier, campaignID int64) (content.Values, error) {
	return longest(ctx, q, `SELECT DISTINCT user_id AS id FROM deliveries WHERE campaign_id = $1`, campaignID)
}
