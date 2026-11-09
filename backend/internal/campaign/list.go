package campaign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Page size limits of List.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// ListFilter selects which campaigns List returns.
type ListFilter string

const (
	FilterAll   ListFilter = "all"
	FilterDraft ListFilter = "draft" // drafts only
	FilterSent  ListFilter = "sent"  // approved, sending or completed
)

// ErrBadFilter: the filter is not one of the ListFilter values.
var ErrBadFilter = errors.New("unknown campaign list filter")

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

// List returns campaigns newest first. before > 0 returns only ids below
// it (keyset paging: a new campaign does not shift the pages). limit is
// clamped to [1, MaxListLimit]; 0 means DefaultListLimit.
//
// Two queries, not one per campaign: the page of campaigns, then the
// delivery counts of the whole page grouped by campaign, channel and
// status. The second one uses the (campaign_id, user_id, channel) key.
func (s *Service) List(ctx context.Context, filter ListFilter, before int64, limit int) (ListPage, error) {
	var where string
	switch filter {
	case FilterAll, "":
		where = "true"
	case FilterDraft:
		where = "status = '" + string(StatusDraft) + "'"
	case FilterSent:
		where = "status <> '" + string(StatusDraft) + "'"
	default:
		return ListPage{}, ErrBadFilter
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)

	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()

	// One extra row tells whether another page exists.
	rows, err := s.db.Query(ctx, `
SELECT id, status, created_at, approved_at, scheduled_at
FROM campaigns
WHERE `+where+` AND ($1 = 0 OR id < $1)
ORDER BY id DESC
LIMIT $2`, before, limit+1)
	if err != nil {
		return ListPage{}, fmt.Errorf("list campaigns: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		it := ListItem{Channels: map[string]ChannelProgress{}}
		err := row.Scan(&it.ID, &it.Status, &it.CreatedAt, &it.ApprovedAt, &it.SendAt)
		return it, err
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
