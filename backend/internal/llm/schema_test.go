package llm

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// jsonFields returns the JSON names of a struct's fields.
func jsonFields(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func obj(t *testing.T, v any, path string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: not an object", path)
	}
	return m
}

// checkObject asserts the schema object lists exactly the struct's fields,
// requires all of them and forbids additional properties (API requirement).
func checkObject(t *testing.T, path string, schema map[string]any, want []string) map[string]any {
	t.Helper()
	props := obj(t, schema["properties"], path+".properties")
	if got := keys(props); !slices.Equal(got, want) {
		t.Errorf("%s: properties %v, struct has %v", path, got, want)
	}
	if schema["additionalProperties"] != false {
		t.Errorf("%s: additionalProperties must be false", path)
	}
	var req []string
	for _, r := range schema["required"].([]any) {
		req = append(req, r.(string))
	}
	sort.Strings(req)
	if !slices.Equal(req, keys(props)) {
		t.Errorf("%s: required %v, want all properties %v", path, req, keys(props))
	}
	return props
}

func enumOf(t *testing.T, v any, path string) []string {
	t.Helper()
	var out []string
	for _, e := range obj(t, v, path)["enum"].([]any) {
		out = append(out, e.(string))
	}
	sort.Strings(out)
	return out
}

func sorted[T ~string](vs ...T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

// TestSegmentSchemaMatchesRule keeps the hand-written schema and
// segment.Rule in sync: same field names and the same enum values as the Go
// constants. If someone adds a field or an enum value on one side only, this
// fails.
func TestSegmentSchemaMatchesRule(t *testing.T) {
	s := SegmentSchema()
	root := checkObject(t, "rule", s, jsonFields(reflect.TypeFor[segment.Rule]()))

	if got, want := enumOf(t, root["status"], "status"), sorted(segment.StatusOK, segment.StatusPartial, segment.StatusUnsupported, segment.StatusRejected); !slices.Equal(got, want) {
		t.Errorf("status enum %v, want %v", got, want)
	}
	if got, want := enumOf(t, root["audience_scope"], "audience_scope"), sorted(segment.ScopeFiltered, segment.ScopeAll); !slices.Equal(got, want) {
		t.Errorf("audience_scope enum %v, want %v", got, want)
	}

	group := obj(t, obj(t, root["groups"], "groups")["items"], "groups.items")
	checkObject(t, "group", group, jsonFields(reflect.TypeFor[segment.Group]()))

	unsup := obj(t, obj(t, root["unsupported"], "unsupported")["items"], "unsupported.items")
	uprops := checkObject(t, "unsupported", unsup, jsonFields(reflect.TypeFor[segment.Unsupported]()))
	wantReasons := sorted(segment.ReasonSequence, segment.ReasonAggregate, segment.ReasonRanking, segment.ReasonUnknownField,
		segment.ReasonAbsoluteDate, segment.ReasonScheduling, segment.ReasonContradiction, segment.ReasonTooComplex, segment.ReasonOffTopic)
	if got := enumOf(t, uprops["reason"], "reason"); !slices.Equal(got, wantReasons) {
		t.Errorf("reason enum %v, want %v", got, wantReasons)
	}

	// Condition: three variants. Together they must cover exactly the
	// fields of segment.Condition, and each variant must use valid values.
	cond := obj(t, obj(t, s["$defs"], "$defs")["condition"], "condition")
	variants := cond["anyOf"].([]any)
	if len(variants) != 3 {
		t.Fatalf("condition has %d variants, want 3 (city, age, event)", len(variants))
	}
	union := map[string]bool{}
	for i, v := range variants {
		vm := obj(t, v, "variant")
		props := obj(t, vm["properties"], "variant.properties")
		checkObject(t, "condition variant", vm, keys(props))
		for k := range props {
			union[k] = true
		}
		kind := obj(t, props["type"], "type")["const"]
		switch i {
		case 0:
			if kind != string(segment.KindAttribute) || obj(t, props["field"], "field")["const"] != string(segment.FieldCity) {
				t.Errorf("variant 0 must be the city attribute")
			}
			if got := enumOf(t, props["op"], "op"); !slices.Equal(got, sorted(segment.OpIn, segment.OpNotIn)) {
				t.Errorf("city ops %v", got)
			}
		case 1:
			if kind != string(segment.KindAttribute) || obj(t, props["field"], "field")["const"] != string(segment.FieldAge) {
				t.Errorf("variant 1 must be the age attribute")
			}
			if got := enumOf(t, props["op"], "op"); !slices.Equal(got, sorted(segment.OpGte, segment.OpLte)) {
				t.Errorf("age ops %v", got)
			}
		case 2:
			if kind != string(segment.KindEvent) {
				t.Errorf("variant 2 must be the event condition")
			}
			want := sorted(segment.EventSignup, segment.EventProductView, segment.EventAddToCart, segment.EventPurchase)
			if got := enumOf(t, props["event"], "event"); !slices.Equal(got, want) {
				t.Errorf("events %v, want %v", got, want)
			}
		}
	}
	unionKeys := make([]string, 0, len(union))
	for k := range union {
		unionKeys = append(unionKeys, k)
	}
	sort.Strings(unionKeys)
	if want := jsonFields(reflect.TypeFor[segment.Condition]()); !slices.Equal(unionKeys, want) {
		t.Errorf("condition variants cover %v, struct has %v", unionKeys, want)
	}
}

// TestSegmentSchemaHasNoUnsupportedKeywords: the API rejects numeric and
// string length constraints (D-LLM-OUT); those checks live in the backend.
func TestSegmentSchemaHasNoUnsupportedKeywords(t *testing.T) {
	banned := []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems"}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if slices.Contains(banned, k) && !strings.HasSuffix(path, ".properties") {
					t.Errorf("%s uses unsupported keyword %q", path, k)
				}
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range x {
				walk(path+"[]", child)
			}
		}
	}
	walk("schema", SegmentSchema())
}

func TestSegmentSchemaReturnsCopy(t *testing.T) {
	a := SegmentSchema()
	a["type"] = "changed"
	if SegmentSchema()["type"] != "object" {
		t.Fatal("SegmentSchema must return an independent copy")
	}
}

func TestDecodeRuleStrict(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid", `{"status":"ok","audience_scope":"all","common":[],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`, false},
		{"unknown top-level field", `{"status":"ok","audience_scope":"all","sql":"DELETE FROM users"}`, true},
		{"unknown condition field", `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["Bursa"],"column":"x"}]}`, true},
		{"trailing data", `{"status":"ok","audience_scope":"all"} {"status":"ok"}`, true},
		{"truncated", `{"status":"ok","audience_scope":"fil`, true},
		{"wrong type", `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"age","op":"gte","value":"25"}]}`, true},
		{"not json", `Here is your rule: {}`, true},
		{"empty", ``, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeRule(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
