package mockprovider

import (
	"sync"
	"time"
)

// window counts the requests of one channel over a sliding one-second
// window (D-MOCK). Every received request is counted, including the ones it
// rejects, so maxSeen is what a real provider would measure: the most
// requests the client sent in any one-second span (D-RATE check).
//
// A sliding window has no boundary to exploit, unlike a fixed per-second
// counter, which lets up to 2×limit through around a second boundary.
type window struct {
	mu      sync.Mutex
	limit   int
	times   []time.Time // arrival times within the last second, oldest first
	maxSeen int
}

func newWindow(limit int) *window {
	return &window{limit: limit}
}

// add records a request arriving at now and reports whether it is within
// the limit, i.e. fewer than limit requests arrived in (now-1s, now).
func (w *window) add(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	cutoff := now.Add(-time.Second)
	drop := 0
	for drop < len(w.times) && !w.times[drop].After(cutoff) {
		drop++
	}
	// Copy down instead of reslicing, so the backing array does not grow
	// without bound under steady traffic.
	w.times = w.times[:copy(w.times, w.times[drop:])]

	allowed := len(w.times) < w.limit
	w.times = append(w.times, now)
	// The count of a window ending at an arrival is the largest of any
	// window around it, so checking at each arrival finds the true maximum.
	w.maxSeen = max(w.maxSeen, len(w.times))
	return allowed
}

// max returns the most requests seen in any one-second window.
func (w *window) max() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maxSeen
}
