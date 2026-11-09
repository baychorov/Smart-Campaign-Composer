package llm

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

func ptr[T any](v T) *T { return &v }

func city(op segment.Op, values ...string) segment.Condition {
	return segment.Condition{Type: segment.KindAttribute, Field: segment.FieldCity, Op: op, Values: values}
}

func age(op segment.Op, v int) segment.Condition {
	return segment.Condition{Type: segment.KindAttribute, Field: segment.FieldAge, Op: op, Value: ptr(v)}
}

func event(ev segment.EventType, occurred bool, days int) segment.Condition {
	return segment.Condition{Type: segment.KindEvent, Event: ev, Occurred: ptr(occurred), WithinDays: ptr(days)}
}

// thenEvent: "ev in the window, then (not) next".
func thenEvent(c segment.Condition, next segment.EventType, occurred bool) segment.Condition {
	c.Then = &segment.Then{Event: next, Occurred: ptr(occurred)}
	return c
}

func atLeast(c segment.Condition, n int) segment.Condition {
	c.MinCount = ptr(n)
	return c
}

func between(ev segment.EventType, occurred bool, from, to string) segment.Condition {
	return segment.Condition{Type: segment.KindEvent, Event: ev, Occurred: ptr(occurred), From: from, To: to}
}

func group(cs ...segment.Condition) segment.Group { return segment.Group{Conditions: cs} }

// mustJSON renders a rule the way the model would send it: every list
// present (the schema requires them), never null.
func mustJSON(t *testing.T, r segment.Rule) string {
	t.Helper()
	nonNil := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	if r.Common == nil {
		r.Common = []segment.Condition{}
	}
	if r.Groups == nil {
		r.Groups = []segment.Group{}
	}
	if r.Unsupported == nil {
		r.Unsupported = []segment.Unsupported{}
	}
	r.Assumptions, r.Notes = nonNil(r.Assumptions), nonNil(r.Notes)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertSameAudience compares the conditions of two rules after both are
// normalised by segment.Validate, so the comparison ignores spelling and
// ordering fixes that Validate makes anyway.
func assertSameAudience(t *testing.T, got segment.Rule, want segment.Rule) {
	t.Helper()
	w := segment.Validate(want)
	if !w.Valid() {
		t.Fatalf("test bug: expected rule is invalid: %v", w.Errors)
	}
	if !reflect.DeepEqual(got.Common, w.Rule.Common) {
		t.Errorf("common:\n got %s\nwant %s", jsonString(got.Common), jsonString(w.Rule.Common))
	}
	if !reflect.DeepEqual(got.Groups, w.Rule.Groups) {
		t.Errorf("groups:\n got %s\nwant %s", jsonString(got.Groups), jsonString(w.Rule.Groups))
	}
	if got.AudienceScope != w.Rule.AudienceScope {
		t.Errorf("audience_scope = %q, want %q", got.AudienceScope, w.Rule.AudienceScope)
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func hasCode(issues []segment.Issue, code string) bool {
	for _, is := range issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

// testToday is the date segment prompts are built with in tests.
var testToday = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// withSendAt adds "send_at" to a reply produced by mustJSON; empty is null.
func withSendAt(t *testing.T, reply, sendAt string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(reply), &m); err != nil {
		t.Fatal(err)
	}
	m["send_at"] = nil
	if sendAt != "" {
		m["send_at"] = sendAt
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func formatSendAt(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.In(segment.Istanbul).Format(time.RFC3339)
}
