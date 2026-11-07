package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
)

// Config tunes the worker.
type Config struct {
	// Rate is messages per second per channel. Burst is 1: idle time does
	// not save up tokens for a later spike (D-RATE).
	Rate         float64
	Concurrency  int           // senders per channel
	BatchSize    int           // rows per claim
	Lease        time.Duration // how long a claim is held before others may take it
	PollInterval time.Duration // pause when nothing is due
	Retry        RetryPolicy
}

// DefaultConfig: 95 msg/s keeps the provider-side count under 100 in any
// one-second window despite network jitter (D-RATE). The lease covers a
// whole batch: 50 rows at 95/s take ~0.5 s plus the request timeout.
func DefaultConfig() Config {
	return Config{
		Rate:         95,
		Concurrency:  8,
		BatchSize:    50,
		Lease:        60 * time.Second,
		PollInterval: 500 * time.Millisecond,
		Retry:        DefaultRetryPolicy(),
	}
}

// recordTimeout bounds one result write.
const recordTimeout = 10 * time.Second

// Worker runs one dispatcher and a pool of senders per channel.
type Worker struct {
	store    Store
	provider Provider
	cfg      Config
	log      *slog.Logger
	now      func() time.Time
	rnd      func() float64
	limiters map[Channel]*rate.Limiter
}

// New builds a Worker. now and rnd may be nil (time.Now, rand.Float64).
func New(store Store, provider Provider, cfg Config, log *slog.Logger, now func() time.Time, rnd func() float64) *Worker {
	if now == nil {
		now = time.Now
	}
	if rnd == nil {
		rnd = rand.Float64
	}
	w := &Worker{store: store, provider: provider, cfg: cfg, log: log, now: now, rnd: rnd, limiters: map[Channel]*rate.Limiter{}}
	for _, ch := range Channels {
		w.limiters[ch] = rate.NewLimiter(rate.Limit(cfg.Rate), 1)
	}
	return w
}

// Run sends until ctx is cancelled, then shuts down gracefully: requests
// already sent are finished and recorded, claimed rows not yet sent are
// released. A crash skips all that; the leases then expire and another run
// picks the rows up again.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, ch := range Channels {
		wg.Go(func() { w.runChannel(ctx, ch) })
	}
	wg.Wait()
}

// runChannel is the dispatcher of one channel: it claims batches and hands
// the jobs to the senders. Channels are independent, so a slow or
// throttled email provider does not hold up SMS (D-RATE).
func (w *Worker) runChannel(ctx context.Context, ch Channel) {
	jobs := make(chan Job)
	var senders sync.WaitGroup
	for range w.cfg.Concurrency {
		senders.Go(func() {
			for j := range jobs {
				w.process(ctx, j)
			}
		})
	}
	defer func() {
		close(jobs)
		senders.Wait()
	}()

	for ctx.Err() == nil {
		now := w.now()
		batch, err := w.store.Claim(ctx, ch, w.cfg.BatchSize, now, now.Add(w.cfg.Lease))
		if err != nil {
			if ctx.Err() == nil {
				w.log.Error("claim deliveries", "channel", ch, "err", err)
			}
			w.pause(ctx)
			continue
		}
		if len(batch) == 0 {
			if _, err := w.store.FinishCampaigns(ctx, w.now()); err != nil && ctx.Err() == nil {
				w.log.Error("finish campaigns", "err", err)
			}
			w.pause(ctx)
			continue
		}
		for i, j := range batch {
			select {
			case jobs <- j:
			case <-ctx.Done():
				for _, rest := range batch[i:] {
					w.complete(ctx, rest, Decision{Kind: KindRelease})
				}
				return
			}
		}
	}
}

func (w *Worker) pause(ctx context.Context) {
	t := time.NewTimer(w.cfg.PollInterval)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// process sends one job and records the outcome.
func (w *Worker) process(ctx context.Context, j Job) {
	// R-AUD-2: consent was read at claim time, right before sending.
	if !j.Consent {
		w.complete(ctx, j, Decision{Kind: KindSkipped, Error: "consent withdrawn"})
		return
	}
	msg, err := render(j)
	if err != nil {
		// Approval validated the templates; this is a bug, not a retry case.
		w.complete(ctx, j, Decision{Kind: KindFailed, Error: "invalid template"})
		return
	}
	// The limiter sits right in front of the request, so retries are
	// limited too.
	if err := w.limiters[j.Channel].Wait(ctx); err != nil {
		w.complete(ctx, j, Decision{Kind: KindRelease})
		return
	}

	// Once started, the request runs to the end even during shutdown: an
	// abandoned request would leave its outcome unknown. The provider's own
	// timeout bounds it.
	sendCtx := context.WithoutCancel(ctx)
	res := w.provider.Send(sendCtx, msg)
	w.complete(sendCtx, j, w.cfg.Retry.Decide(res, j, w.rnd))
}

// complete stores a decision. It runs even when ctx is cancelled, so a
// graceful shutdown records what already happened. A failure to store is
// only logged: the row stays "sending", its lease expires and it is sent
// again with the same Idempotency-Key, so the provider does not deliver it
// twice.
func (w *Worker) complete(ctx context.Context, j Job, d Decision) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	ok, err := w.store.Complete(ctx, j, d, w.now())
	// Delivery id and outcome only: never the recipient or the text.
	attrs := []any{"delivery_id", j.ID, "campaign_id", j.CampaignID, "channel", j.Channel,
		"decision", d.Kind, "attempts", j.Attempts, "error", d.Error}
	switch {
	case err != nil:
		w.log.Error("record delivery result", append(attrs, "err", err)...)
	case !ok:
		w.log.Warn("delivery lease lost, result dropped", attrs...)
	case d.Kind == KindFailed:
		w.log.Warn("delivery failed", attrs...)
	default:
		w.log.Debug("delivery processed", attrs...)
	}
}

// render fills the campaign templates for one recipient.
func render(j Job) (Message, error) {
	vals := content.Values{content.VarFirstName: j.FirstName, content.VarCity: j.City}
	m := Message{Channel: j.Channel, IdempotencyKey: strconv.FormatInt(j.ID, 10), To: j.To}
	var err error
	switch j.Channel {
	case ChannelSMS:
		m.Text, err = content.Render(j.SMSText, vals)
	case ChannelEmail:
		if m.Subject, err = content.Render(j.EmailSubject, vals); err == nil {
			m.Body, err = content.Render(j.EmailBody, vals)
		}
	default:
		err = fmt.Errorf("unknown channel %q", j.Channel)
	}
	return m, err
}
