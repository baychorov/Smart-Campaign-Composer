package mockprovider

import (
	"crypto/sha256"
	"sync"
)

// response is a stored reply, replayed for a repeated Idempotency-Key.
type response struct {
	status int
	body   []byte
}

type idemEntry struct {
	payload [sha256.Size]byte // hash of the request body
	done    bool
	resp    response
}

// beginResult is the outcome of idempotency.begin.
type beginResult int

const (
	beginNew        beginResult = iota // first request: process it
	beginReplay                        // finished before: return the stored response
	beginInProgress                    // same key is being processed right now
	beginMismatch                      // same key, different body: a client bug
)

// idempotency stores the result of every delivered message by key
// (D-DEDUP layer 4). Only deliveries are stored: when a request fails
// without delivering (500, injected 429), the key is released so a retry
// is processed afresh. When the mock accepts and then answers 500, the
// delivery is stored, so the retry gets the success and no second message.
type idempotency struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
}

func newIdempotency() *idempotency {
	return &idempotency{entries: map[string]*idemEntry{}}
}

// begin claims key for a request with the given body hash.
func (s *idempotency) begin(key string, payload [sha256.Size]byte) (beginResult, response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	switch {
	case !ok:
		s.entries[key] = &idemEntry{payload: payload}
		return beginNew, response{}
	case e.payload != payload:
		return beginMismatch, response{}
	case !e.done:
		return beginInProgress, response{}
	default:
		return beginReplay, e.resp
	}
}

// finish stores the delivered response of a claimed key.
func (s *idempotency) finish(key string, resp response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		e.done, e.resp = true, resp
	}
}

// release forgets a claimed key whose request delivered nothing.
func (s *idempotency) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok && !e.done {
		delete(s.entries, key)
	}
}
