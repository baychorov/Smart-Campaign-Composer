package campaign

import (
	"context"
	"fmt"
	"time"
)

// rateWindow is the span of the sending rate in Progress.
const rateWindow = 10 * time.Second

// ChannelProgress counts the deliveries of one channel by status.
type ChannelProgress struct {
	Total   int64 `json:"total"`
	Pending int64 `json:"pending"`
	Sending int64 `json:"sending"`
	Sent    int64 `json:"sent"`
	Failed  int64 `json:"failed"`
	Skipped int64 `json:"skipped"`
}

// add counts n deliveries of a status (one row of a GROUP BY status).
func (cp *ChannelProgress) add(status string, n int64) {
	cp.Total += n
	switch status {
	case "pending":
		cp.Pending = n
	case "sending":
		cp.Sending = n
	case "sent":
		cp.Sent = n
	case "failed":
		cp.Failed = n
	case "skipped":
		cp.Skipped = n
	}
}

// ErrorCount is one error class and how many open or failed rows have it.
type ErrorCount struct {
	Error string `json:"error"`
	Count int64  `json:"count"`
}

// Progress is the live state of a campaign's sending (R-OBS-1). It holds
// counts and error classes only, never recipients.
type Progress struct {
	CampaignID int64                      `json:"campaign_id"`
	Status     Status                     `json:"status"`
	Channels   map[string]ChannelProgress `json:"channels"`
	// RatePerSecond is messages sent over the last rateWindow, all channels.
	RatePerSecond float64 `json:"rate_per_second"`
	// TopErrors are the most common last_error values of rows that are not
	// sent: waiting for a retry, or failed for good.
	TopErrors []ErrorCount `json:"top_errors"`
	Done      bool         `json:"done"` // completed or cancelled: nothing more will happen
	At        time.Time    `json:"at"`
}

// Progress reads the current state of a campaign from the database. It is
// polled about once a second per viewer, so each query uses an index:
// deliveries are found through the (campaign_id, user_id, channel) key.
func (s *Service) Progress(ctx context.Context, id int64) (Progress, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	now := s.now()
	p := Progress{CampaignID: id, Channels: map[string]ChannelProgress{}, TopErrors: []ErrorCount{}, At: now}

	c, err := s.Get(ctx, id)
	if err != nil {
		return Progress{}, err
	}
	p.Status, p.Done = c.Status, c.Status == StatusCompleted || c.Status == StatusCancelled

	rows, err := s.db.Query(ctx, `SELECT channel, status, count(*) FROM deliveries
WHERE campaign_id = $1 GROUP BY channel, status`, id)
	if err != nil {
		return Progress{}, fmt.Errorf("count deliveries of campaign %d: %w", id, err)
	}
	for rows.Next() {
		var ch, status string
		var n int64
		if err := rows.Scan(&ch, &status, &n); err != nil {
			rows.Close()
			return Progress{}, fmt.Errorf("scan delivery counts: %w", err)
		}
		cp := p.Channels[ch]
		cp.add(status, n)
		p.Channels[ch] = cp
	}
	if err := rows.Err(); err != nil {
		return Progress{}, fmt.Errorf("count deliveries of campaign %d: %w", id, err)
	}

	var recent int64
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM deliveries
WHERE campaign_id = $1 AND status = 'sent' AND sent_at > $2`, id, now.Add(-rateWindow)).Scan(&recent); err != nil {
		return Progress{}, fmt.Errorf("count recent deliveries of campaign %d: %w", id, err)
	}
	p.RatePerSecond = float64(recent) / rateWindow.Seconds()

	// last_error holds status codes and error classes only (D-RETRY), so
	// it is safe to show.
	erows, err := s.db.Query(ctx, `SELECT last_error, count(*) FROM deliveries
WHERE campaign_id = $1 AND status <> 'sent' AND last_error IS NOT NULL
GROUP BY last_error ORDER BY count(*) DESC, last_error LIMIT 5`, id)
	if err != nil {
		return Progress{}, fmt.Errorf("top errors of campaign %d: %w", id, err)
	}
	defer erows.Close()
	for erows.Next() {
		var e ErrorCount
		if err := erows.Scan(&e.Error, &e.Count); err != nil {
			return Progress{}, fmt.Errorf("scan top errors: %w", err)
		}
		p.TopErrors = append(p.TopErrors, e)
	}
	if err := erows.Err(); err != nil {
		return Progress{}, fmt.Errorf("top errors of campaign %d: %w", id, err)
	}
	return p, nil
}
