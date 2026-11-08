package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// DBAudience returns an AudienceFunc that compiles the rule with the
// production compiler (consent filter included) for each channel and runs
// it on pool. The audience is the users reachable on SMS or email. now
// fixes the "last N days" windows; it must match the seed's now.
func DBAudience(pool *pgxpool.Pool, now time.Time) AudienceFunc {
	return func(ctx context.Context, r segment.Rule) ([]int64, error) {
		ids := []int64{}
		for _, ch := range []segment.Channel{segment.ChannelSMS, segment.ChannelEmail} {
			q, err := segment.CompileAudience(r, ch, now)
			if err != nil {
				return nil, fmt.Errorf("compile: %w", err)
			}
			if err := CheckSQL(q, ch); err != nil {
				return nil, err
			}
			rows, err := pool.Query(ctx, q.SQL, q.Args...)
			if err != nil {
				return nil, fmt.Errorf("query: %w", err)
			}
			chIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return nil, fmt.Errorf("query: %w", err)
			}
			ids = append(ids, chIDs...)
		}
		slices.Sort(ids)
		return slices.Compact(ids), nil
	}
}

// CheckSQL repeats the compiler's guarantees on the actual query text
// (R-AUD-1, R-AUD-2): it starts with the channel's consent filter, and no
// rule value reached the text (no quotes, statement separators or
// non-ASCII letters such as the Turkish city names; values travel only as
// $n parameters). The property tests in package segment prove this for
// random rules; here it is checked for every rule the model produced.
func CheckSQL(q segment.Query, ch segment.Channel) error {
	prefix := "SELECT u.id FROM users u WHERE u." + string(ch) + "_opt_in AND ("
	switch {
	case !strings.HasPrefix(q.SQL, prefix):
		return fmt.Errorf("%w: consent filter missing for %s", ErrUnsafeSQL, ch)
	case strings.ContainsAny(q.SQL, `'";`):
		return fmt.Errorf("%w: quote or separator in SQL text", ErrUnsafeSQL)
	case strings.IndexFunc(q.SQL, func(r rune) bool { return r > unicode.MaxASCII }) >= 0:
		return fmt.Errorf("%w: non-ASCII text in SQL", ErrUnsafeSQL)
	}
	return nil
}
