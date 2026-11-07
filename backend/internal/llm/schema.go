package llm

import (
	"bytes"
	_ "embed" // for the //go:embed directive below
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

// decodeRule parses model output strictly: unknown fields and trailing data
// are errors. Missing fields become zero values; segment.Validate rejects
// those (empty status, missing condition fields and so on).
func decodeRule(text string) (segment.Rule, error) {
	var r segment.Rule
	if err := decodeStrict(text, &r); err != nil {
		return segment.Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	return r, nil
}
