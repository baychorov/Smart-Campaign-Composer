package delivery

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrInvalidConfig is wrapped by every error of ConfigFromEnv.
var ErrInvalidConfig = errors.New("invalid worker config")

// ConfigFromEnv reads WORKER_* variables over DefaultConfig. The settings
// live here, not in platform.Config, because only the worker uses them.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	var errs []error
	if v := getenv("WORKER_SEND_RATE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			errs = append(errs, fmt.Errorf("WORKER_SEND_RATE must be a positive number, got %q", v))
		}
		cfg.Rate = f
	}
	if v := getenv("WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("WORKER_CONCURRENCY must be a positive integer, got %q", v))
		}
		cfg.Concurrency = n
	}
	if v := getenv("WORKER_LEASE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			errs = append(errs, fmt.Errorf("WORKER_LEASE must be a duration of at least 1s, got %q", v))
		}
		cfg.Lease = d
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return cfg, nil
}
