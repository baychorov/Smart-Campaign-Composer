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
	return checkObjectOptional(t, path, schema, want, nil)
}

// checkObjectOptional is checkObject where the fields in optional may be
// left out of required. Optional properties are limited by the API (24 per
// request across schemas), so only the event window, count and sequence
// fields use them.
func checkObjectOptional(t *testing.T, path string, schema map[string]any, want, optional []string) map[string]any {
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
	var wantReq []string
	for _, k := range keys(props) {
		if !slices.Contains(optional, k) {
			wantReq = append(wantReq, k)
		}
	}
	if !slices.Equal(req, wantReq) {
		t.Errorf("%s: required %v, want %v", path, req, wantReq)
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
		// Every field is required. The event qualifiers (window, count,
		// order) are nullable instead of optional: optional properties are
		// written after the required ones, and the model then tended to
		// pick a window or drop a qualifier as an afterthought. Required
		// properties keep the schema's order, so within_days comes first.
		var optional []string
		checkObjectOptional(t, "condition variant", vm, keys(props), optional)
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
			thenAny := obj(t, props["then"], "then")["anyOf"].([]any)
			if len(thenAny) != 2 || obj(t, thenAny[1], "then null")["type"] != "null" {
				t.Errorf("then must be an object or null: %v", thenAny)
			}
			if mc := obj(t, props["min_count"], "min_count")["type"]; !reflect.DeepEqual(mc, []any{"integer", "null"}) {
				t.Errorf("min_count type %v, want integer or null", mc)
			}
			tprops := checkObject(t, "then", obj(t, thenAny[0], "then object"), jsonFields(reflect.TypeFor[segment.Then]()))
			if got := enumOf(t, tprops["event"], "then.event"); !slices.Equal(got, want) {
				t.Errorf("then events %v, want %v", got, want)
			}
			for _, k := range []string{"from", "to"} {
				alt := obj(t, props[k], k)["anyOf"].([]any)
				if obj(t, alt[0], k)["format"] != "date" || obj(t, alt[1], k)["type"] != "null" {
					t.Errorf("%s must be a date or null: %v", k, alt)
				}
			}
			if wd := obj(t, props["within_days"], "within_days")["type"]; !reflect.DeepEqual(wd, []any{"integer", "null"}) {
				t.Errorf("within_days type %v, want integer or null", wd)
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

// The model writes null for an unused count or order; strict decoding must
// accept that and leave the fields unset.
func TestDecodeRuleNullQualifiers(t *testing.T) {
	text := `{"status":"ok","audience_scope":"filtered","common":[` +
		`{"type":"event","event":"purchase","occurred":true,"within_days":30,"from":null,"to":null,"min_count":null,"then":null},` +
		`{"type":"event","event":"add_to_cart","occurred":true,"within_days":null,"from":"2026-10-01","to":"2026-10-15","min_count":null,"then":{"event":"purchase","occurred":false}}` +
		`],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
	r, err := decodeRule(text)
	if err != nil {
		t.Fatal(err)
	}
	if c := r.Common[0]; c.MinCount != nil || c.Then != nil {
		t.Errorf("null qualifiers decoded as %+v", c)
	}
	if c := r.Common[1]; c.Then == nil || c.Then.Event != segment.EventPurchase {
		t.Errorf("then not decoded: %+v", c)
	}
	if v := segment.Validate(r); !v.Valid() {
		t.Errorf("validation: %v", v.Errors)
	}
}
