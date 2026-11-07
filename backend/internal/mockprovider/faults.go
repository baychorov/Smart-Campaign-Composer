package mockprovider

import (
	"math/rand/v2"
	"sync"
	"time"
)

// outcome is what the mock does with a new (non-replayed) request.
type outcome int

const (
	outcomeDeliver        outcome = iota // 202, message delivered
	outcomeError                         // 500, nothing delivered
	outcomeThrottle                      // 429 with Retry-After, nothing delivered
	outcomeAcceptThenFail                // message delivered, but the reply is 500
)

func (o outcome) String() string {
	return [...]string{"deliver", "error", "throttle", "accept_then_fail"}[o]
}

// faults draws outcomes and delays from a seeded RNG (R-MOCK-2). With the
// same seed and the same order of requests the sequence repeats.
type faults struct {
	mu  sync.Mutex
	rng *rand.Rand
	cfg Config
}

func newFaults(cfg Config) *faults {
	return &faults{rng: rand.New(rand.NewPCG(cfg.Seed, cfg.Seed)), cfg: cfg}
}

// draw picks the outcome and the response delay of one request. One
// uniform number is split into bands, so the rates are exact
// probabilities that do not overlap.
func (f *faults) draw() (outcome, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var delay time.Duration
	if f.cfg.DelayMax > 0 {
		delay = time.Duration(f.rng.Int64N(int64(f.cfg.DelayMax) + 1))
	}
	u := f.rng.Float64()
	switch {
	case u < f.cfg.ErrorRate:
		return outcomeError, delay
	case u < f.cfg.ErrorRate+f.cfg.ThrottleRate:
		return outcomeThrottle, delay
	case u < f.cfg.ErrorRate+f.cfg.ThrottleRate+f.cfg.AcceptThenFailRate:
		return outcomeAcceptThenFail, delay
	default:
		return outcomeDeliver, delay
	}
}
