package llm

import (
	"bytes"
	_ "embed" // for the //go:embed directive below
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// segmentSchemaJSON is the output_config.format schema for segment
// extraction. It is hand-written (D-LLM-OUT); TestSegmentSchemaMatchesRule
// keeps it in sync with segment.Rule. go:embed compiles the file into the
// binary, so there is nothing to ship next to it.
//
//go:embed segment_schema.json
var segmentSchemaJSON []byte

// contentSchemaJSON is the output_config.format schema for message
// generation. TestContentSchemaMatchesMessage keeps it in sync with
// content.Message.
//
//go:embed content_schema.json
var contentSchemaJSON []byte

// SegmentSchema returns a fresh copy of the segment schema, so a caller can
// never mutate the shared one.
func SegmentSchema() map[string]any { return loadSchema(segmentSchemaJSON) }

// ContentSchema returns a fresh copy of the content schema.
func ContentSchema() map[string]any { return loadSchema(contentSchemaJSON) }

func loadSchema(raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("embedded schema is invalid: %v", err)) // programmer error
	}
	return m
}

// decodeStrict parses model output into v: unknown fields and trailing
// data are errors.
func decodeStrict(text string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON object")
	}
	return nil
}

// segmentOutput is the model's answer: the audience rule plus the send time
// it read from the brief. The send time is not part of segment.Rule because
// it says nothing about the audience; the campaign stores it separately.
type segmentOutput struct {
	segment.Rule
	SendAt *string `json:"send_at"`
}

// decodeSegmentOutput parses model output strictly: unknown fields and
// trailing data are errors. Missing fields become zero values;
// segment.Validate rejects those (empty status, missing condition fields
// and so on).
func decodeSegmentOutput(text string) (segmentOutput, error) {
	var out segmentOutput
	if err := decodeStrict(text, &out); err != nil {
		return segmentOutput{}, fmt.Errorf("decode rule: %w", err)
	}
	return out, nil
}

// decodeRule is decodeSegmentOutput without the send time.
func decodeRule(text string) (segment.Rule, error) {
	out, err := decodeSegmentOutput(text)
	return out.Rule, err
}

// sendAtLayouts are the forms a suggested send time is accepted in. The
// schema asks for RFC 3339; the others are lenient fallbacks, read as
// Turkey time when there is no offset.
var sendAtLayouts = []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05", "2006-01-02T15:04"}

// parseSendAt reads the model's suggested send time. A suggestion that
// cannot be read is dropped, not repaired: the marketer picks the time on
// the approval screen anyway.
func parseSendAt(s *string) (*time.Time, bool) {
	if s == nil || *s == "" {
		return nil, true
	}
	for _, layout := range sendAtLayouts {
		if t, err := time.ParseInLocation(layout, *s, segment.Istanbul); err == nil {
			return &t, true
		}
	}
	return nil, false
}
