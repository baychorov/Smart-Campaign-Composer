package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Page size limits of List.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// List statuses: the statuses the list can be filtered by. They are the
// campaign statuses, except that a draft is split in two: ListDraft is a
// draft that can be approved, ListInvalid one whose brief gave no
// approvable audience (it can only be deleted). The UI shows the same split.
const (
	ListDraft   = "draft"
	ListInvalid = "invalid"
)

// listStatuses are the accepted ListQuery.Statuses values.
var listStatuses = []string{ListDraft, ListInvalid,
	string(StatusApproved), string(StatusScheduled), string(StatusSending), string(StatusCompleted), string(StatusCancelled)}

// approvableSQL is true for a stored rule that Approvable accepts. A stored
// rule is always valid (Create stores no invalid model output, UpdateRule
// refuses an invalid edit), so its status alone decides.
var approvableSQL = "coalesce(rule->>'status', '') IN ('" + string(segment.StatusOK) + "', '" + string(segment.StatusPartial) + "')"

// ListQuery selects the campaigns List returns. Zero values do not filter.
type ListQuery struct {
	// Statuses keeps campaigns with any of these list statuses; empty
	// means all.
	Statuses []string
	// ID keeps only the campaign with this id.
	ID int64
	// From and To keep campaigns whose list date (approval time, or
	// creation time for a draft) is in [From, To).
	From, To time.Time
	// Before > 0 returns only ids below it (keyset paging: a new campaign
	// does not shift the pages).
	Before int64
	// Limit is clamped to [1, MaxListLimit]; 0 means DefaultListLimit.
	Limit int
}

// ErrBadQuery: the list query has an unknown status or an empty date range.
var ErrBadQuery = errors.New("bad campaign list query")

// where builds the query's WHERE clause. Only fixed SQL text is written;
// every value is a $n parameter, numbered from 1, returned in args.
func (q ListQuery) where() (string, []any, error) {
	var conds []string
	var args []any
	param := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	if len(q.Statuses) > 0 {
		var ors, plain []string
		for _, st := range q.Statuses {
			switch {
			case st == ListDraft:
				ors = append(ors, "(status = '"+string(StatusDraft)+"' AND "+approvableSQL+")")
			case st == ListInvalid:
				ors = append(ors, "(status = '"+string(StatusDraft)+"' AND NOT "+approvableSQL+")")
			case slices.Contains(listStatuses, st):
				plain = append(plain, st)
			default:
				return "", nil, fmt.Errorf("%w: status %q", ErrBadQuery, st)
			}
		}
		if len(plain) > 0 {
			ors = append(ors, "status = ANY("+param(plain)+")")
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	if q.ID > 0 {
		conds = append(conds, "id = "+param(q.ID))
	}
	if !q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To) {
		return "", nil, fmt.Errorf("%w: empty date range", ErrBadQuery)
	}
	if !q.From.IsZero() {
		conds = append(conds, "coalesce(approved_at, created_at) >= "+param(q.From))
	}
	if !q.To.IsZero() {
		conds = append(conds, "coalesce(approved_at, created_at) < "+param(q.To))
	}
	if q.Before > 0 {
		conds = append(conds, "id < "+param(q.Before))
	}
	if len(conds) == 0 {
		return "true", args, nil
	}
	return strings.Join(conds, " AND "), args, nil
}

// ListItem is one row of the campaign list: what the collapsed row shows.
// The brief, rule and message are left out on purpose; the UI reads them
// with Get when the marketer opens the row.
type ListItem struct {
	ID         int64      `json:"id"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ApprovedAt *time.Time `json:"approved_at"`
	// SendAt is the scheduled send time of a scheduled or cancelled
	// campaign, or the suggestion of a draft.
	SendAt *time.Time `json:"send_at"`
	// Approvable tells whether the stored rule can be approved. A draft
	// whose brief gave no approvable audience (unsupported or rejected)
	// stays a draft that can only be deleted; the list marks it. The value
	// cannot change later: such a rule has no conditions to edit.
	Approvable bool `json:"approvable"`
	// Channels has the delivery counts of each channel chosen at approval;
	// empty for a draft.
	Channels map[string]ChannelProgress `json:"progress"`
}

// ListPage is one page of List, newest first. NextBefore is the cursor of
// the next page (pass it as before), or 0 when there is none.
type ListPage struct {
	Items      []ListItem
	NextBefore int64
}

// List returns the campaigns that match q, newest first.
//
// Two queries, not one per campaign: the page of campaigns, then the
// delivery counts of the whole page grouped by campaign, channel and
// status. The second one uses the (campaign_id, user_id, channel) key.
func (s *Service) List(ctx context.Context, q ListQuery) (ListPage, error) {
	where, args, err := q.where()
	if err != nil {
		return ListPage{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)

	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	// One extra row tells whether another page exists.
	rows, err := s.db.Query(ctx, `
SELECT id, status, created_at, approved_at, scheduled_at, rule
FROM campaigns
WHERE `+where+`
ORDER BY id DESC
LIMIT `+"$"+strconv.Itoa(len(args)+1), append(args, limit+1)...)
	if err != nil {
		return ListPage{}, fmt.Errorf("list campaigns: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		it := ListItem{Channels: map[string]ChannelProgress{}}
		var raw []byte
		if err := row.Scan(&it.ID, &it.Status, &it.CreatedAt, &it.ApprovedAt, &it.SendAt, &raw); err != nil {
			return it, err
		}
		var rule segment.Rule
		if err := json.Unmarshal(raw, &rule); err != nil {
			return it, fmt.Errorf("decode stored rule of campaign %d: %w", it.ID, err)
		}
		it.Approvable = segment.Validate(rule).Approvable()
		return it, nil
	})
	if err != nil {
		return ListPage{}, fmt.Errorf("scan campaigns: %w", err)
	}

	var page ListPage
	if len(items) > limit {
		items = items[:limit]
		page.NextBefore = items[limit-1].ID
	}
	page.Items = items
	if err := s.addDeliveryCounts(ctx, items); err != nil {
		return ListPage{}, err
	}
	return page, nil
}

// addDeliveryCounts fills Channels of the approved items. Drafts have no
// deliveries and are skipped.
func (s *Service) addDeliveryCounts(ctx context.Context, items []ListItem) error {
	index := map[int64]int{}
	ids := []int64{}
	for i, it := range items {
		if it.Status != StatusDraft {
			index[it.ID] = i
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `
SELECT campaign_id, channel, status, count(*)
FROM deliveries
WHERE campaign_id = ANY($1)
GROUP BY campaign_id, channel, status`, ids)
	if err != nil {
		return fmt.Errorf("count deliveries of campaign list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, n int64
		var ch, status string
		if err := rows.Scan(&id, &ch, &status, &n); err != nil {
			return fmt.Errorf("scan delivery counts: %w", err)
		}
		it := &items[index[id]]
		cp := it.Channels[ch]
		cp.add(status, n)
		it.Channels[ch] = cp
	}
	return rows.Err()
}

// DeleteDraft deletes a draft campaign. An approved campaign cannot be
// deleted (ErrNotDraft): its deliveries are the record of what was sent.
//
// The status check and the delete are one statement, so a concurrent
// approval and delete cannot both succeed: the approval's UPDATE ... WHERE
// status = 'draft' locks the row, and the DELETE re-checks the status after
// that lock is released.
func (s *Service) DeleteDraft(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	tag, err := s.db.Exec(ctx, `DELETE FROM campaigns WHERE id = $1 AND status = '`+string(StatusDraft)+`'`, id)
	if err != nil {
		return fmt.Errorf("delete campaign %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return notDraftReason(ctx, s.db, id)
	}
	return nil
}
