package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
)

// IdealClient is an llm.Client that answers every request with the
// IdealReply of the case whose brief is in the prompt. make eval -dry-run
// uses it to exercise the whole eval offline; tests set Override to inject
// wrong answers.
type IdealClient struct {
	cases []Case
	// Override, if set, may replace the reply text of the n-th call (from
	// 0) for a case.
	Override func(caseID, n int) (reply string, ok bool)

	mu    sync.Mutex
	calls map[int]int
}

// NewIdealClient returns an IdealClient for cases.
func NewIdealClient(cases []Case) *IdealClient {
	return &IdealClient{cases: cases, calls: map[int]int{}}
}

// Stream implements llm.Client.
func (c *IdealClient) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	var found *Case
	for i := range c.cases {
		// Repair prompts also start with the brief, so this matches them too.
		if strings.Contains(req.Prompt, "<brief>\n"+c.cases[i].Brief+"\n</brief>") {
			found = &c.cases[i]
			break
		}
	}
	if found == nil {
		return llm.Response{}, fmt.Errorf("ideal client: no case matches the prompt")
	}

	c.mu.Lock()
	n := c.calls[found.ID]
	c.calls[found.ID]++
	c.mu.Unlock()

	var text string
	if reply, ok := c.override(found.ID, n); ok {
		text = reply
	} else {
		b, err := json.Marshal(IdealReply(*found))
		if err != nil {
			return llm.Response{}, err
		}
		text = string(b)
	}
	if onDelta != nil {
		onDelta(text)
	}
	return llm.Response{Text: text, StopReason: llm.StopEndTurn}, nil
}

func (c *IdealClient) override(id, n int) (string, bool) {
	if c.Override == nil {
		return "", false
	}
	return c.Override(id, n)
}
