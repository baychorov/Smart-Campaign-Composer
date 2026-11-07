package httpapi

import (
	"crypto/rand"
	"sync"
	"time"
)

// generation holds the events of one content generation in memory
// (D-STREAM: single API replica). Events are only appended, so a client
// that connects late or reconnects replays them from any position.
type generation struct {
	created time.Time

	mu      sync.Mutex
	events  []sseEvent
	done    bool
	changed chan struct{} // closed and replaced on every append
}

func newGeneration(now time.Time) *generation {
	return &generation{created: now, changed: make(chan struct{})}
}

// append adds an event; final marks the generation as finished. Waiting
// readers are woken by closing the changed channel (a broadcast).
func (g *generation) append(e sseEvent, final bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return
	}
	g.events = append(g.events, e)
	g.done = final
	close(g.changed)
	g.changed = make(chan struct{})
}

// since returns the events from index from on, whether the generation is
// finished, and a channel that is closed on the next append.
func (g *generation) since(from int) ([]sseEvent, bool, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var evs []sseEvent
	if from < len(g.events) {
		evs = append(evs, g.events[from:]...)
	}
	return evs, g.done, g.changed
}

// registry maps ids to generations. Finished generations are dropped after
// ttl, checked whenever a new one starts.
type registry struct {
	mu   sync.Mutex
	gens map[string]*generation
	ttl  time.Duration
	now  func() time.Time
}

func newRegistry(ttl time.Duration, now func() time.Time) *registry {
	return &registry{gens: map[string]*generation{}, ttl: ttl, now: now}
}

// create registers a new generation under a random, unguessable id.
func (r *registry) create() (string, *generation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for id, g := range r.gens {
		g.mu.Lock()
		expired := g.done && now.Sub(g.created) > r.ttl
		g.mu.Unlock()
		if expired {
			delete(r.gens, id)
		}
	}
	id := rand.Text()
	g := newGeneration(now)
	r.gens[id] = g
	return id, g
}

func (r *registry) get(id string) (*generation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gens[id]
	return g, ok
}

func (r *registry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.gens)
}
