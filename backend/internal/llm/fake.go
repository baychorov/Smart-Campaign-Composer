package llm

import (
	"context"
	"fmt"
	"sync"
)

// FakeResponse is one scripted reply of Fake.
type FakeResponse struct {
	// Chunks are delivered to onDelta in order; Response.Text is their
	// concatenation. Splitting the text lets tests cut multi-byte characters
	// or escape sequences across chunks.
	Chunks     []string
	StopReason StopReason // empty means StopEndTurn
	Refusal    *Refusal
	Usage      Usage
	// Err, if set, is returned after the chunks have been delivered. Wrap
	// ErrStreamInterrupted to simulate a connection drop mid-stream.
	Err error
}

// Fake is a scripted Client for tests and LLM_PROVIDER=mock. It replies in
// order and records every request. It is safe for concurrent use.
type Fake struct {
	mu        sync.Mutex
	responses []FakeResponse
	calls     []Request
}

// NewFake returns a Fake that answers with responses in order.
func NewFake(responses ...FakeResponse) *Fake {
	return &Fake{responses: responses}
}

// Text is a shortcut for a complete end_turn reply delivered in one chunk.
func Text(s string) FakeResponse {
	return FakeResponse{Chunks: []string{s}}
}

// Stream implements Client. It fails when the script is exhausted, so a test
// that makes more calls than expected fails loudly.
func (f *Fake) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	if n > len(f.responses) {
		f.mu.Unlock()
		return Response{}, fmt.Errorf("fake llm: unexpected call %d, only %d scripted", n, len(f.responses))
	}
	fr := f.responses[n-1]
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	var text string
	for _, c := range fr.Chunks {
		text += c
		if onDelta != nil {
			onDelta(c)
		}
	}
	if fr.Err != nil {
		return Response{}, fr.Err
	}
	stop := fr.StopReason
	if stop == "" {
		stop = StopEndTurn
	}
	return Response{Text: text, StopReason: stop, Usage: fr.Usage, Refusal: fr.Refusal}, nil
}

// Calls returns a copy of the requests received so far.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}
