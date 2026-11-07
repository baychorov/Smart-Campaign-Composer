package llm

import (
	"context"
	"fmt"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
)

// Token limits for message generation (~300-600 output tokens).
const (
	contentMaxTokens      = 8000
	contentRetryMaxTokens = 32000
)

// ContentEventType names the stream events of message generation
// (D-STREAM): started → delta… → validated | invalid, with reset whenever
// the text shown so far must be discarded (stream restart, max_tokens
// retry, repair).
type ContentEventType string

const (
	ContentStarted   ContentEventType = "started"
	ContentDelta     ContentEventType = "delta"
	ContentReset     ContentEventType = "reset"
	ContentValidated ContentEventType = "validated"
	ContentInvalid   ContentEventType = "invalid"
)

// ContentEvent is one event. Field and Text are set for deltas, Result for
// validated, Issues for invalid.
type ContentEvent struct {
	Type   ContentEventType
	Field  string
	Text   string
	Result *content.Result
	Issues []content.Issue
}

// ContentResult is the outcome of GenerateContent.
type ContentResult struct {
	// Validation is the validated message; meaningful only with a nil error.
	Validation content.Result
	// Issues are the errors of the last failed attempt (ErrInvalidOutput).
	Issues   []content.Issue
	Repairs  int
	Attempts []Attempt
}

// Usage sums the tokens of all attempts.
func (r ContentResult) Usage() Usage { return sumUsage(r.Attempts) }

// GenerateContent writes SMS and email copy and streams it through emit as
// it is generated. The text shown by the deltas is exactly the text that
// is validated: the extractor decodes the same JSON the strict decoder
// reads. longest holds the longest value of each variable in the audience
// for the worst-case length check (D-CONTENT).
//
// emit is called synchronously from this goroutine. A terminal event
// (validated or invalid) is emitted unless an error other than
// ErrInvalidOutput is returned; the caller reports those.
func GenerateContent(ctx context.Context, c Client, in ContentInput, longest content.Values, emit func(ContentEvent)) (ContentResult, error) {
	var res ContentResult
	brief, err := CheckBrief(in.Brief)
	if err != nil {
		return res, err
	}
	in.Brief = brief

	emit(ContentEvent{Type: ContentStarted})
	prompt := contentPrompt(in, longest)
	for repair := 0; ; repair++ {
		x := content.NewExtractor(content.Fields...)
		onDelta := func(chunk string) {
			for _, d := range x.Write(chunk) {
				emit(ContentEvent{Type: ContentDelta, Field: d.Field, Text: d.Text})
			}
		}
		onReset := func() {
			x = content.NewExtractor(content.Fields...)
			emit(ContentEvent{Type: ContentReset})
		}
		req := Request{
			System:    contentSystemPrompt,
			Prompt:    prompt,
			Schema:    ContentSchema(),
			MaxTokens: contentMaxTokens,
			Effort:    EffortLow,
		}
		resp, err := generate(ctx, c, req, contentRetryMaxTokens, repair, &res.Attempts, onDelta, onReset)
		if err != nil {
			return res, fmt.Errorf("generate content: %w", err)
		}

		var m content.Message
		if err := decodeStrict(resp.Text, &m); err == nil {
			res.Validation = content.Validate(m, longest)
			if res.Validation.Valid() {
				res.Issues = nil
				v := res.Validation
				emit(ContentEvent{Type: ContentValidated, Result: &v})
				return res, nil
			}
			res.Issues = res.Validation.Errors
		} else {
			res.Validation = content.Result{}
			res.Issues = []content.Issue{{Code: IssueInvalidJSON, Message: "decode message: " + err.Error()}}
		}

		recordIssues(res.Attempts, res.Issues, func(i content.Issue) string { return i.Code })
		if repair == MaxRepairs {
			emit(ContentEvent{Type: ContentInvalid, Issues: res.Issues})
			return res, fmt.Errorf("generate content: %w", ErrInvalidOutput)
		}
		res.Repairs++
		emit(ContentEvent{Type: ContentReset})
		prompt = contentRepairPrompt(in, longest, resp.Text, res.Issues)
	}
}
