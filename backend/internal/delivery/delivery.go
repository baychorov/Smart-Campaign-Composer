// Package delivery sends queued deliveries to the SMS and email providers
// (R-SEND-1..4). The deliveries table is the queue (D-DB); the worker claims
// rows with a lease, sends them through a per-channel rate limiter (D-RATE),
// and records the outcome or schedules a retry (D-RETRY). Every request
// carries the delivery id as Idempotency-Key, so a resend after a crash
// does not produce a second message (D-DEDUP).
package delivery

import (
	"context"
	"time"
)

// Channel is a delivery channel. Values match deliveries.channel.
type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

// Channels lists every channel; each has its own loop and limiter.
var Channels = []Channel{ChannelSMS, ChannelEmail}

// Job is one claimed delivery with what is needed to send it.
type Job struct {
	ID         int64
	CampaignID int64
	Channel    Channel
	// Attempts after this claim (the claim counts as an attempt). It is also
	// the fencing token: a result is stored only if the row still has this
	// value, so a worker whose lease expired cannot overwrite a newer claim.
	Attempts  int
	Throttles int

	// Recipient data, read at claim time. Consent is re-checked right
	// before sending (R-AUD-2): a withdrawn consent makes the job skipped.
	Consent   bool
	To        string // phone or email; never logged
	FirstName string
	City      string

	// Campaign copy (templates with {{variables}}).
	SMSText      string
	EmailSubject string
	EmailBody    string
}

// Message is a rendered message as sent to the provider.
type Message struct {
	Channel        Channel
	IdempotencyKey string
	To             string
	Text           string // SMS
	Subject        string // email
	Body           string // email
}

// SendResult is what the provider call returned. Err is set for transport
// failures (timeout, connection refused); then StatusCode is 0.
type SendResult struct {
	StatusCode int
	RetryAfter time.Duration // from the Retry-After header of a 429
	Err        error
}

// Provider sends one message. Implemented over HTTP by HTTPProvider; tests
// use fakes to inject failures.
type Provider interface {
	Send(ctx context.Context, m Message) SendResult
}

// Store is the queue. Implemented on Postgres by PGStore; tests wrap it to
// inject failures such as "sent, then crashed before recording".
type Store interface {
	// Claim leases up to limit due rows of a channel until leaseUntil.
	Claim(ctx context.Context, ch Channel, limit int, now, leaseUntil time.Time) ([]Job, error)
	// Complete records the decision for a claimed job. It reports false if
	// the lease was lost (another claim took the row); nothing is written.
	Complete(ctx context.Context, job Job, d Decision, now time.Time) (bool, error)
	// FinishCampaigns marks sending campaigns with no open rows completed.
	FinishCampaigns(ctx context.Context, now time.Time) (int64, error)
}
