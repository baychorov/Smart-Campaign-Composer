package segment

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func codes(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		rule      Rule
		wantErrs  []string // distinct codes, any order
		wantWarns []string
		check     func(t *testing.T, r Rule) // optional check of the normalised rule
	}{
		// --- valid rules -------------------------------------------------
		{name: "independent cart and purchase (D-CART)", rule: cartNoPurchaseRule()},
		{
			name: "all users",
			rule: Rule{Status: StatusOK, AudienceScope: ScopeAll},
		},
		{
			name: "two groups with common (brief 17)",
			rule: okRule([]Condition{event(EventAddToCart, true, 30)},
				group(city(OpIn, "İstanbul"), age(OpLte, 24)),
				group(city(OpIn, "Ankara"), age(OpGte, 41))),
		},
		{
			name: "did in 30 days but not in last 7 is satisfiable",
			rule: okRule([]Condition{event(EventPurchase, true, 30), event(EventPurchase, false, 7)}),
		},
		{
			name: "partial with scheduling (brief 13)",
			rule: Rule{
				Status: StatusPartial, AudienceScope: ScopeFiltered,
				Common:      []Condition{city(OpIn, "Bursa"), event(EventPurchase, true, 30)},
				Unsupported: []Unsupported{{Text: "yarın sabah 10'da", Reason: ReasonScheduling}},
			},
		},
		{
			name: "unsupported without conditions (brief 12)",
			rule: Rule{
				Status: StatusUnsupported, AudienceScope: ScopeFiltered,
				Unsupported: []Unsupported{{Text: "iPhone kullanan VIP", Reason: ReasonUnknownField}},
			},
		},
		{
			name: "rejected (brief 14)",
			rule: Rule{
				Status: StatusRejected, AudienceScope: ScopeFiltered,
				Unsupported: []Unsupported{{Text: "sıralama algoritması", Reason: ReasonOffTopic}},
			},
		},
		{
			name: "valid province without users",
			rule: okRule([]Condition{city(OpIn, "Bolu")}),
		},

		// --- automatic fixes (warnings) -----------------------------------
		{
			name: "upper-case enums",
			rule: Rule{
				Status: "OK", AudienceScope: "FILTERED",
				Common: []Condition{
					{Type: "ATTRIBUTE", Field: "CITY", Op: "IN", Values: []string{"İstanbul"}},
					{Type: "EVENT", Event: "ADD_TO_CART", Occurred: ptr(true), WithinDays: ptr(30)},
				},
			},
			wantWarns: []string{CodeNormalizedEnum},
			check: func(t *testing.T, r Rule) {
				if r.Status != StatusOK || r.Common[1].Event != EventAddToCart {
					t.Errorf("enums not normalised: %+v", r)
				}
			},
		},
		{
			name:      "city spelling (brief 7)",
			rule:      okRule([]Condition{city(OpIn, "istanbul", "IZMIR"), age(OpLte, 29)}),
			wantWarns: []string{CodeNormalizedCity},
			check: func(t *testing.T, r Rule) {
				if got := r.Common[0].Values; !slices.Equal(got, []string{"İstanbul", "İzmir"}) {
					t.Errorf("cities = %q", got)
				}
			},
		},
		{
			name:      "duplicate city value",
			rule:      okRule([]Condition{city(OpIn, "Ankara", "ankara")}),
			wantWarns: []string{CodeDuplicateValue, CodeNormalizedCity},
			check: func(t *testing.T, r Rule) {
				if got := r.Common[0].Values; !slices.Equal(got, []string{"Ankara"}) {
					t.Errorf("cities = %q", got)
				}
			},
		},
		{
			name:      "duplicate condition",
			rule:      okRule([]Condition{city(OpIn, "Ankara"), city(OpIn, "Ankara")}),
			wantWarns: []string{CodeDuplicateCondition},
			check: func(t *testing.T, r Rule) {
				if len(r.Common) != 1 {
					t.Errorf("common = %+v", r.Common)
				}
			},
		},
		{
			name:      "single group moves to common",
			rule:      okRule([]Condition{age(OpGte, 18)}, group(city(OpIn, "Ankara"))),
			wantWarns: []string{CodeSingleGroup},
			check: func(t *testing.T, r Rule) {
				if len(r.Groups) != 0 || len(r.Common) != 2 {
					t.Errorf("not merged: %+v", r)
				}
			},
		},
		{
			name: "covered group is dropped (brief 20)",
			rule: okRule(nil,
				group(city(OpIn, "İstanbul")),
				group(city(OpIn, "İstanbul"), age(OpLte, 24))),
			wantWarns: []string{CodeCoveredGroup, CodeSingleGroup},
			check: func(t *testing.T, r Rule) {
				want := []Condition{city(OpIn, "İstanbul")}
				if len(r.Groups) != 0 || !reflect.DeepEqual(r.Common, want) {
					t.Errorf("got common=%+v groups=%+v", r.Common, r.Groups)
				}
			},
		},

		// --- deliberately broken LLM output (PLAN test set) ---------------
		{
			name:     "filtered scope with no conditions",
			rule:     Rule{Status: StatusOK, AudienceScope: ScopeFiltered},
			wantErrs: []string{CodeEmptyRule},
		},
		{
			name:     "all scope with conditions",
			rule:     Rule{Status: StatusOK, AudienceScope: ScopeAll, Common: []Condition{city(OpIn, "Ankara")}},
			wantErrs: []string{CodeScopeMismatch},
		},
		{
			name: "ok with unsupported items",
			rule: Rule{
				Status: StatusOK, AudienceScope: ScopeFiltered, Common: []Condition{city(OpIn, "Ankara")},
				Unsupported: []Unsupported{{Text: "x", Reason: ReasonAggregate}},
			},
			wantErrs: []string{CodeStatusMismatch},
		},
		{
			name:     "partial without unsupported items",
			rule:     Rule{Status: StatusPartial, AudienceScope: ScopeFiltered, Common: []Condition{city(OpIn, "Ankara")}},
			wantErrs: []string{CodeStatusMismatch},
		},
		{
			name:     "rejected with conditions",
			rule:     Rule{Status: StatusRejected, AudienceScope: ScopeFiltered, Common: []Condition{city(OpIn, "Ankara")}},
			wantErrs: []string{CodeStatusMismatch},
		},
		{
			name:     "contradiction: did and did not in same window (brief 9)",
			rule:     okRule([]Condition{event(EventPurchase, true, 30), event(EventPurchase, false, 30)}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:     "contradiction: did not in a wider window",
			rule:     okRule([]Condition{event(EventPurchase, true, 7), event(EventPurchase, false, 30)}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:     "contradiction: age range",
			rule:     okRule([]Condition{age(OpGte, 40), age(OpLte, 30)}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:      "contradiction: city in and not_in",
			rule:      okRule([]Condition{city(OpIn, "İstanbul"), city(OpNotIn, "istanbul")}),
			wantErrs:  []string{CodeContradiction},
			wantWarns: []string{CodeNormalizedCity}, // compared after normalisation
		},
		{
			name:     "contradiction: two disjoint city lists",
			rule:     okRule([]Condition{city(OpIn, "İstanbul"), city(OpIn, "Ankara")}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name: "contradiction between common and one group",
			rule: okRule([]Condition{age(OpGte, 40)},
				group(city(OpIn, "Ankara")),
				group(age(OpLte, 30))),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:      "identical groups",
			rule:      okRule(nil, group(city(OpIn, "Ankara"), age(OpGte, 30)), group(age(OpGte, 30), city(OpIn, "ankara"))),
			wantErrs:  []string{CodeDuplicateGroup},
			wantWarns: []string{CodeNormalizedCity}, // compared after normalisation
		},

		// --- malformed conditions -----------------------------------------
		{name: "invalid status", rule: Rule{Status: "done", AudienceScope: ScopeAll}, wantErrs: []string{CodeInvalidEnum}},
		{name: "invalid scope", rule: Rule{Status: StatusOK, AudienceScope: "some"}, wantErrs: []string{CodeInvalidEnum}},
		{
			name:     "unknown field",
			rule:     okRule([]Condition{{Type: KindAttribute, Field: "loyalty", Op: OpIn, Values: []string{"gold"}}}),
			wantErrs: []string{CodeInvalidEnum},
		},
		{
			name:     "unknown event",
			rule:     okRule([]Condition{event("refund", true, 30)}),
			wantErrs: []string{CodeInvalidEnum},
		},
		{
			name:     "unknown condition type",
			rule:     okRule([]Condition{{Type: "sql", Field: FieldCity}}),
			wantErrs: []string{CodeInvalidEnum},
		},
		{
			name:     "unknown city",
			rule:     okRule([]Condition{city(OpIn, "Paris")}),
			wantErrs: []string{CodeUnknownCity},
		},
		{
			name:     "SQL in a city value",
			rule:     okRule([]Condition{city(OpIn, "İstanbul') OR TRUE --")}),
			wantErrs: []string{CodeUnknownCity},
		},
		{
			name:     "city with numeric op",
			rule:     okRule([]Condition{{Type: KindAttribute, Field: FieldCity, Op: OpGte, Values: []string{"Ankara"}}}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{
			name:     "city without values",
			rule:     okRule([]Condition{city(OpIn)}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{
			name:     "age with values",
			rule:     okRule([]Condition{{Type: KindAttribute, Field: FieldAge, Op: OpGte, Values: []string{"30"}}}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{name: "age below range", rule: okRule([]Condition{age(OpGte, 12)}), wantErrs: []string{CodeOutOfRange}},
		{name: "age above range", rule: okRule([]Condition{age(OpLte, 101)}), wantErrs: []string{CodeOutOfRange}},
		{name: "window zero", rule: okRule([]Condition{event(EventPurchase, true, 0)}), wantErrs: []string{CodeOutOfRange}},
		{name: "window too long", rule: okRule([]Condition{event(EventPurchase, true, 366)}), wantErrs: []string{CodeOutOfRange}},
		{
			name:     "event without occurred",
			rule:     okRule([]Condition{{Type: KindEvent, Event: EventPurchase, WithinDays: ptr(30)}}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{
			name:     "attribute with event fields",
			rule:     okRule([]Condition{{Type: KindAttribute, Field: FieldCity, Op: OpIn, Values: []string{"Ankara"}, WithinDays: ptr(3)}}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{
			name:     "empty group",
			rule:     okRule(nil, group(city(OpIn, "Ankara")), group()),
			wantErrs: []string{CodeInvalidCondition},
		},
		{
			name:     "invalid unsupported reason",
			rule:     Rule{Status: StatusUnsupported, AudienceScope: ScopeFiltered, Unsupported: []Unsupported{{Text: "x", Reason: "other"}}},
			wantErrs: []string{CodeInvalidEnum},
		},
		{
			name: "unsupported group out of range",
			rule: Rule{
				Status: StatusPartial, AudienceScope: ScopeFiltered, Common: []Condition{city(OpIn, "Ankara")},
				Unsupported: []Unsupported{{Text: "x", Reason: ReasonAggregate, Group: ptr(0)}},
			},
			wantErrs: []string{CodeInvalidUnsupport},
		},

		// --- limits (too_complex) -----------------------------------------
		{
			name: "six groups (brief 19)",
			rule: okRule(nil,
				group(city(OpIn, "İstanbul"), age(OpLte, 20)), group(city(OpIn, "Ankara"), age(OpLte, 25)),
				group(city(OpIn, "İzmir"), age(OpLte, 30)), group(city(OpIn, "Bursa"), age(OpLte, 35)),
				group(city(OpIn, "Antalya"), age(OpLte, 40)), group(city(OpIn, "Konya"), age(OpLte, 45))),
			wantErrs: []string{CodeTooComplex},
		},
		{
			name:     "seven common conditions",
			rule:     okRule(repeatAges(7)),
			wantErrs: []string{CodeTooComplex},
		},
		{
			name: "more than twenty conditions in total",
			rule: okRule(repeatAges(6),
				group(repeatAges(6)...), group(repeatAges(5)...), group(repeatAges(4)...)),
			wantErrs: []string{CodeTooComplex},
		},
		{
			name:     "twenty-one city values",
			rule:     okRule([]Condition{city(OpIn, provinces[:21]...)}),
			wantErrs: []string{CodeTooComplex},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Validate(tt.rule)
			if got := codes(res.Errors); !slices.Equal(got, sortedCopy(tt.wantErrs)) {
				t.Errorf("errors = %v, want %v\n%+v", got, tt.wantErrs, res.Errors)
			}
			if got := codes(res.Warnings); !slices.Equal(got, sortedCopy(tt.wantWarns)) {
				t.Errorf("warnings = %v, want %v\n%+v", got, tt.wantWarns, res.Warnings)
			}
			if tt.check != nil {
				tt.check(t, res.Rule)
			}
		})
	}
}

// repeatAges returns n distinct, mutually compatible age conditions.
func repeatAges(n int) []Condition {
	var cs []Condition
	for i := range n {
		cs = append(cs, age(OpGte, 18+i))
	}
	return cs
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

func TestValidateDoesNotModifyInput(t *testing.T) {
	in := okRule([]Condition{city(OpIn, "istanbul", "IZMIR")}, group(age(OpGte, 18)))
	before := cloneRule(in)
	_ = Validate(in)
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("input changed:\n got %+v\nwant %+v", in, before)
	}
}

func TestUnsupportedGroupIndexFollowsNormalisation(t *testing.T) {
	r := Rule{
		Status: StatusPartial, AudienceScope: ScopeFiltered,
		Groups: []Group{
			group(city(OpIn, "İstanbul"), age(OpLte, 24)), // covered by group 2, dropped
			group(city(OpIn, "İstanbul")),
			group(city(OpIn, "Ankara")),
		},
		Unsupported: []Unsupported{
			{Text: "dropped group", Reason: ReasonAggregate, Group: ptr(0)},
			{Text: "ankara group", Reason: ReasonAggregate, Group: ptr(2)},
		},
	}
	res := Validate(r)
	if !res.Valid() {
		t.Fatalf("errors: %+v", res.Errors)
	}
	if g := res.Rule.Unsupported[0].Group; g != nil {
		t.Errorf("dropped group index = %d, want nil", *g)
	}
	if g := res.Rule.Unsupported[1].Group; g == nil || *g != 1 {
		t.Errorf("ankara group index = %v, want 1", g)
	}
}

func TestIssueMessagesAreTurkishAndNonEmpty(t *testing.T) {
	res := Validate(okRule([]Condition{city(OpIn, "Paris"), age(OpGte, 5)}))
	for _, is := range res.Errors {
		if strings.TrimSpace(is.Message) == "" || is.Path == "" {
			t.Errorf("incomplete issue: %+v", is)
		}
	}
}
