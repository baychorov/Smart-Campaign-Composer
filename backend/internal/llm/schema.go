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

// SegmentSchema returns a fresh copy of the segment schema, so a caller can
// never mutate the shared one.
func SegmentSchema() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(segmentSchemaJSON, &m); err != nil {
		panic(fmt.Sprintf("embedded segment schema is invalid: %v", err)) // programmer error
	}
	return m
}

// decodeRule parses model output strictly: unknown fields and trailing data
// are errors. Missing fields become zero values; segment.Validate rejects
// those (empty status, missing condition fields and so on).
func decodeRule(text string) (segment.Rule, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	var r segment.Rule
	if err := dec.Decode(&r); err != nil {
		return segment.Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return segment.Rule{}, errors.New("decode rule: unexpected data after the JSON object")
	}
	return r, nil
}
