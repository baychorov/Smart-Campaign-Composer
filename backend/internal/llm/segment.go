package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Retry limits from D-LLM-RETRY.
const (
	// MaxRepairs is how many times an invalid rule is sent back with its
	// errors. With the first attempt that is at most 3 validated outputs.
	MaxRepairs = 2
	// maxStreamRestarts bounds restarts after a mid-stream disconnect.
	maxStreamRestarts = 1
	// MaxBriefRunes caps the brief length; longer input is refused before
	// any API call.
	MaxBriefRunes = 2000
)

// Token limits for segment extraction. The rule is ~200 output tokens; the
// headroom covers adaptive thinking. A max_tokens stop is retried once with
// the larger limit.
const (
	segmentMaxTokens      = 8000
	segmentRetryMaxTokens = 32000
)

// IssueInvalidJSON is the issue code for output that is not a strict JSON
// rule (unknown field, wrong type, trailing data).
const IssueInvalidJSON = "invalid_json"

var (
	// ErrInvalidBrief: empty or too long brief. No API call is made.
	ErrInvalidBrief = errors.New("invalid brief")
	// ErrTruncated: the output hit max_tokens even after the retry with a
	// higher limit. Truncated output is never parsed (R-AI-1).
	ErrTruncated = errors.New("llm output truncated by max_tokens")
	// ErrUnexpectedStop: a stop reason other than end_turn, max_tokens or
	// refusal (e.g. pause_turn), which this single-turn call cannot handle.
	ErrUnexpectedStop = errors.New("llm unexpected stop reason")
	// ErrInvalidOutput: the rule was still invalid after MaxRepairs repairs.
	// SegmentResult.Issues holds the last errors; approval stays closed.
	ErrInvalidOutput = errors.New("llm output invalid after repairs")
)

// RefusalError is returned for stop_reason "refusal". The output is not
// parsed; the explanation is shown to the marketer (D-LLM-RETRY).
type RefusalError struct {
	Refusal Refusal
}

func (e *RefusalError) Error() string {
	return fmt.Sprintf("llm refused the request (category %q)", e.Refusal.Category)
}

// Attempt records one call for logs and metrics (R-OBS-2). Error is empty
// when the call succeeded at the transport level.
type Attempt struct {
	Repair     int // 0 for the first request, then 1..MaxRepairs
	StopReason StopReason
	Usage      Usage
	Error      string
	// IssueCodes are the validation error codes of this attempt's output
	// (invalid_json for undecodable output); empty when it was valid.
	IssueCodes []string
}

// recordIssues stores the codes of the last attempt's validation errors.
func recordIssues[T any](attempts []Attempt, issues []T, code func(T) string) {
	if len(attempts) == 0 {
		return
	}
	codes := make([]string, len(issues))
	for i, is := range issues {
		codes[i] = code(is)
	}
	attempts[len(attempts)-1].IssueCodes = codes
}

// SegmentResult is the outcome of ExtractSegment.
type SegmentResult struct {
	// Validation is the validated, normalised rule. Only meaningful when
	// ExtractSegment returns a nil error; use Validation.Rule, not the raw
	// model output, for compiling and display.
	Validation segment.Result
	// Issues are the errors of the last failed attempt (with
	// ErrInvalidOutput).
	Issues []segment.Issue
	// Repairs is how many repair requests were sent.
	Repairs  int
	Attempts []Attempt
}

// Usage sums the tokens of all attempts.
func (r SegmentResult) Usage() Usage { return sumUsage(r.Attempts) }

func sumUsage(attempts []Attempt) Usage {
	var u Usage
	for _, a := range attempts {
		u.InputTokens += a.Usage.InputTokens
		u.OutputTokens += a.Usage.OutputTokens
	}
	return u
}

// CheckBrief trims the brief and enforces MaxBriefRunes. It is run before
// any call; handlers use it to reject a bad brief synchronously.
func CheckBrief(brief string) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidBrief)
	}
	if n := utf8.RuneCountInString(brief); n > MaxBriefRunes {
		return "", fmt.Errorf("%w: %d characters, at most %d", ErrInvalidBrief, n, MaxBriefRunes)
	}
	return brief, nil
}

// ExtractSegment turns a brief into a validated segment rule:
//
//	call → (refusal / max_tokens / stream checks) → strict decode →
//	segment.Validate → on errors, up to MaxRepairs single-turn repairs.
//
// A nil error means res.Validation.Valid() is true. The rule may still have
// status unsupported or rejected; res.Validation.Approvable() tells whether
// it describes an audience. today anchors relative dates in the brief
// ("Ekim'de", "geçen ay").
func ExtractSegment(ctx context.Context, c Client, brief string, today time.Time) (SegmentResult, error) {
	var res SegmentResult
	brief, err := CheckBrief(brief)
	if err != nil {
		return res, err
	}

	prompt := segmentPrompt(brief, today)
	for repair := 0; ; repair++ {
		req := Request{
			System:    segmentSystemPrompt,
			Prompt:    prompt,
			Schema:    SegmentSchema(),
			MaxTokens: segmentMaxTokens,
			Effort:    EffortLow,
		}
		resp, err := generate(ctx, c, req, segmentRetryMaxTokens, repair, &res.Attempts, nil, nil)
		if err != nil {
			return res, fmt.Errorf("extract segment: %w", err)
		}

		rule, err := decodeRule(resp.Text)
		if err == nil {
			res.Validation = segment.Validate(rule)
			if res.Validation.Valid() {
				res.Issues = nil
				return res, nil
			}
			res.Issues = res.Validation.Errors
		} else {
			res.Validation = segment.Result{}
			res.Issues = []segment.Issue{{Code: IssueInvalidJSON, Message: err.Error()}}
		}

		recordIssues(res.Attempts, res.Issues, func(i segment.Issue) string { return i.Code })
		if repair == MaxRepairs {
			return res, fmt.Errorf("extract segment: %w", ErrInvalidOutput)
		}
		res.Repairs++
		prompt = repairPrompt(brief, today, resp.Text, res.Issues)
	}
}

// generate runs one logical request and returns only complete end_turn
// output. It handles the cases that must not reach the parser (D-LLM-RETRY):
//   - mid-stream disconnect: restart from scratch (onReset tells a streaming
//     UI to clear what it showed), at most maxStreamRestarts times
//   - max_tokens: retry once with retryMaxTokens, else ErrTruncated
//   - refusal: *RefusalError, never parsed
//
// 429/5xx are retried inside the SDK; 400/401 come back as *APIError and are
// not retried here.
func generate(ctx context.Context, c Client, req Request, retryMaxTokens int64, repair int,
	attempts *[]Attempt, onDelta func(string), onReset func()) (Response, error) {
	restarts := 0
	raisedLimit := false
	reset := func() {
		if onReset != nil {
			onReset()
		}
	}

	for {
		resp, err := c.Stream(ctx, req, onDelta)
		a := Attempt{Repair: repair, StopReason: resp.StopReason, Usage: resp.Usage}
		if err != nil {
			a.Error = err.Error()
		}
		*attempts = append(*attempts, a)

		if err != nil {
			if errors.Is(err, ErrStreamInterrupted) && restarts < maxStreamRestarts && ctx.Err() == nil {
				restarts++
				reset()
				continue
			}
			return Response{}, err
		}

		switch resp.StopReason {
		case StopEndTurn:
			return resp, nil
		case StopRefusal:
			r := Refusal{}
			if resp.Refusal != nil {
				r = *resp.Refusal
			}
			return Response{}, &RefusalError{Refusal: r}
		case StopMaxTokens:
			if raisedLimit || req.MaxTokens >= retryMaxTokens {
				return Response{}, ErrTruncated
			}
			raisedLimit = true
			req.MaxTokens = retryMaxTokens
			reset()
		default:
			return Response{}, fmt.Errorf("%w: %q", ErrUnexpectedStop, resp.StopReason)
		}
	}
}
