package mockprovider

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// Config tunes the mock's behaviour. It lives here, not in platform.Config,
// because only this fake service uses it.
type Config struct {
	ErrorRate          float64       // share of new requests answered 500, nothing delivered
	ThrottleRate       float64       // share answered 429 at random, besides the rate limit
	AcceptThenFailRate float64       // share delivered but answered 500
	DelayMax           time.Duration // each reply waits a random 0..DelayMax
	RateLimit          int           // requests per channel in any one-second window
	RetryAfter         time.Duration // value of the Retry-After header on 429
	Seed               uint64        // RNG seed; the same seed repeats the fault sequence
}

// DefaultConfig: occasional faults, as R-MOCK-2 asks, and the 100 msg/s
// limit of R-SEND-2.
func DefaultConfig() Config {
	return Config{
		ErrorRate:          0.05,
		ThrottleRate:       0.03,
		AcceptThenFailRate: 0.02,
		DelayMax:           50 * time.Millisecond,
		RateLimit:          100,
		RetryAfter:         time.Second,
		Seed:               42,
	}
}

// ErrInvalidConfig is wrapped by every error of ConfigFromEnv.
var ErrInvalidConfig = errors.New("invalid mock provider config")

// ConfigFromEnv reads MOCK_* variables over DefaultConfig. Tests pass a map
// lookup instead of os.Getenv.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	var errs []error
	rate := func(key string, dst *float64) {
		if v := getenv(key); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 1 {
				errs = append(errs, fmt.Errorf("%s must be a number in [0, 1], got %q", key, v))
				return
			}
			*dst = f
		}
	}
	duration := func(key string, dst *time.Duration) {
		if v := getenv(key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				errs = append(errs, fmt.Errorf("%s must be a duration like 50ms, got %q", key, v))
				return
			}
			*dst = d
		}
	}

	rate("MOCK_ERROR_RATE", &cfg.ErrorRate)
	rate("MOCK_429_RATE", &cfg.ThrottleRate)
	rate("MOCK_ACCEPT_THEN_FAIL_RATE", &cfg.AcceptThenFailRate)
	duration("MOCK_DELAY_MAX", &cfg.DelayMax)
	duration("MOCK_RETRY_AFTER", &cfg.RetryAfter)
	if v := getenv("MOCK_RATE_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("MOCK_RATE_LIMIT must be a positive integer, got %q", v))
		}
		cfg.RateLimit = n
	}
	if v := getenv("MOCK_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("MOCK_SEED must be a non-negative integer, got %q", v))
		}
		cfg.Seed = n
	}
	if sum := cfg.ErrorRate + cfg.ThrottleRate + cfg.AcceptThenFailRate; sum > 1 {
		errs = append(errs, fmt.Errorf("fault rates add up to %.2f, at most 1", sum))
	}
	if cfg.RetryAfter < time.Second {
		// Retry-After is sent in whole seconds.
		errs = append(errs, fmt.Errorf("MOCK_RETRY_AFTER must be at least 1s, got %s", cfg.RetryAfter))
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return cfg, nil
}

// LogValue prints durations as text (slog would print nanoseconds).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Float64("error_rate", c.ErrorRate),
		slog.Float64("throttle_rate", c.ThrottleRate),
		slog.Float64("accept_then_fail_rate", c.AcceptThenFailRate),
		slog.String("delay_max", c.DelayMax.String()),
		slog.Int("rate_limit", c.RateLimit),
		slog.String("retry_after", c.RetryAfter.String()),
		slog.Uint64("seed", c.Seed),
	)
}
