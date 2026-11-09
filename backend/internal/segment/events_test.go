package segment

import (
	"slices"
	"testing"
)

// Validation of the event forms added in PLAN step 14b: count, calendar
// range and sequence. Same table shape as TestValidate.
func TestValidateEventForms(t *testing.T) {
	withCount := func(c Condition, n int) Condition { c.MinCount = ptr(n); return c }
	withDays := func(c Condition, d int) Condition { c.WithinDays = ptr(d); return c }

	tests := []struct {
		name      string
		rule      Rule
		wantErrs  []string
		wantWarns []string
		check     func(t *testing.T, r Rule)
	}{
		// --- valid ---------------------------------------------------------
		{name: "case brief as a sequence", rule: caseRule()},
		{name: "at least 3 purchases", rule: okRule([]Condition{city(OpIn, "İzmir"), countEvent(EventPurchase, 3, 90)})},
		{name: "calendar range", rule: okRule([]Condition{dateEvent(EventProductView, true, "2026-10-01", "2026-10-15")})},
		{name: "single day", rule: okRule([]Condition{dateEvent(EventSignup, true, "2026-11-01", "2026-11-01")})},
		{name: "count in a calendar range", rule: okRule([]Condition{withCount(dateEvent(EventPurchase, true, "2026-10-01", "2026-10-31"), 2)})},
		{name: "sequence in a calendar range", rule: okRule([]Condition{{
			Type: KindEvent, Event: EventAddToCart, Occurred: ptr(true), From: "2026-10-01", To: "2026-10-31",
			Then: &Then{Event: EventPurchase, Occurred: ptr(false)},
		}})},
		{
			name: "did in October but not 5-10 October is satisfiable",
			rule: okRule([]Condition{
				dateEvent(EventPurchase, true, "2026-10-01", "2026-10-31"),
				dateEvent(EventPurchase, false, "2026-10-05", "2026-10-10"),
			}),
		},
		{
			// The regression this key guards against: if MinCount were not
			// part of the condition key, these two would be "duplicates" and
			// one would be dropped.
			name: "plain and count conditions on the same event are both kept",
			rule: okRule([]Condition{event(EventPurchase, true, 90), countEvent(EventPurchase, 3, 90)}),
			check: func(t *testing.T, r Rule) {
				if len(r.Common) != 2 {
					t.Errorf("common = %+v", r.Common)
				}
			},
		},
		{
			name: "a group with a count is not covered by the plain group",
			rule: okRule(nil,
				group(event(EventPurchase, true, 90)),
				group(countEvent(EventPurchase, 3, 90), city(OpIn, "İzmir"))),
			check: func(t *testing.T, r Rule) {
				if len(r.Groups) != 2 {
					t.Errorf("groups = %+v", r.Groups)
				}
			},
		},
		{
			name: "sequences with different second events are both kept",
			rule: okRule([]Condition{
				seqEvent(EventAddToCart, 30, EventPurchase, false),
				seqEvent(EventAddToCart, 30, EventProductView, false),
			}),
			check: func(t *testing.T, r Rule) {
				if len(r.Common) != 2 {
					t.Errorf("common = %+v", r.Common)
				}
			},
		},

		// --- automatic fixes -------------------------------------------------
		{
			name:      "min_count 1 is dropped",
			rule:      okRule([]Condition{countEvent(EventPurchase, 1, 30)}),
			wantWarns: []string{CodeNormalizedCount},
			check: func(t *testing.T, r Rule) {
				if r.Common[0].MinCount != nil {
					t.Errorf("min_count kept: %+v", r.Common[0])
				}
			},
		},
		{
			name:      "identical count conditions are deduplicated",
			rule:      okRule([]Condition{countEvent(EventPurchase, 3, 90), countEvent(EventPurchase, 3, 90)}),
			wantWarns: []string{CodeDuplicateCondition},
		},
		{
			name: "upper-case then event",
			rule: okRule([]Condition{{
				Type: KindEvent, Event: EventAddToCart, Occurred: ptr(true), WithinDays: ptr(30),
				Then: &Then{Event: "PURCHASE", Occurred: ptr(false)},
			}}),
			wantWarns: []string{CodeNormalizedEnum},
			check: func(t *testing.T, r Rule) {
				if r.Common[0].Then.Event != EventPurchase {
					t.Errorf("then = %+v", r.Common[0].Then)
				}
			},
		},

		// --- window errors ---------------------------------------------------
		{name: "days and dates together", rule: okRule([]Condition{withDays(dateEvent(EventPurchase, true, "2026-10-01", "2026-10-02"), 30)}), wantErrs: []string{CodeInvalidCondition}},
		{name: "no window", rule: okRule([]Condition{{Type: KindEvent, Event: EventPurchase, Occurred: ptr(true)}}), wantErrs: []string{CodeInvalidCondition}},
		{name: "only from", rule: okRule([]Condition{dateEvent(EventPurchase, true, "2026-10-01", "")}), wantErrs: []string{CodeInvalidCondition}},
		{name: "Turkish date format", rule: okRule([]Condition{dateEvent(EventPurchase, true, "01.10.2026", "15.10.2026")}), wantErrs: []string{CodeInvalidCondition}},
		{name: "impossible date", rule: okRule([]Condition{dateEvent(EventPurchase, true, "2026-02-30", "2026-03-01")}), wantErrs: []string{CodeInvalidCondition}},
		{name: "from after to", rule: okRule([]Condition{dateEvent(EventPurchase, true, "2026-10-15", "2026-10-01")}), wantErrs: []string{CodeInvalidCondition}},
		{name: "range longer than a year", rule: okRule([]Condition{dateEvent(EventPurchase, true, "2025-01-01", "2026-01-02")}), wantErrs: []string{CodeOutOfRange}},

		// --- count errors ----------------------------------------------------
		{name: "count with did not", rule: okRule([]Condition{withCount(event(EventPurchase, false, 30), 3)}), wantErrs: []string{CodeInvalidCondition}},
		{name: "count on signup", rule: okRule([]Condition{countEvent(EventSignup, 2, 30)}), wantErrs: []string{CodeInvalidCondition}},
		{name: "count above the limit", rule: okRule([]Condition{countEvent(EventPurchase, 101, 30)}), wantErrs: []string{CodeOutOfRange}},
		{name: "count zero", rule: okRule([]Condition{countEvent(EventPurchase, 0, 30)}), wantErrs: []string{CodeOutOfRange}},
		{name: "count on an attribute", rule: okRule([]Condition{withCount(age(OpGte, 18), 3)}), wantErrs: []string{CodeInvalidCondition}},

		// --- sequence errors -------------------------------------------------
		{
			name:     "then after did not",
			rule:     okRule([]Condition{{Type: KindEvent, Event: EventAddToCart, Occurred: ptr(false), WithinDays: ptr(30), Then: &Then{Event: EventPurchase, Occurred: ptr(false)}}}),
			wantErrs: []string{CodeInvalidCondition},
		},
		{name: "then with count", rule: okRule([]Condition{withCount(seqEvent(EventAddToCart, 30, EventPurchase, false), 2)}), wantErrs: []string{CodeInvalidCondition}},
		{name: "then on the same event", rule: okRule([]Condition{seqEvent(EventPurchase, 30, EventPurchase, false)}), wantErrs: []string{CodeInvalidCondition}},
		{name: "then unknown event", rule: okRule([]Condition{seqEvent(EventAddToCart, 30, "refund", false)}), wantErrs: []string{CodeInvalidEnum}},
		{
			name:     "then without occurred",
			rule:     okRule([]Condition{{Type: KindEvent, Event: EventAddToCart, Occurred: ptr(true), WithinDays: ptr(30), Then: &Then{Event: EventPurchase}}}),
			wantErrs: []string{CodeInvalidCondition},
		},

		// --- limits ------------------------------------------------------------
		{
			// The model kept 5 of 6 groups and listed the sixth as too_complex
			// (seen in make eval, brief 19): the whole brief must be refused.
			name: "a trimmed rule with too_complex",
			rule: Rule{
				Status: StatusPartial, AudienceScope: ScopeFiltered,
				Groups:      []Group{group(city(OpIn, "İstanbul")), group(city(OpIn, "Ankara"))},
				Unsupported: []Unsupported{{Text: "6. grup", Reason: ReasonTooComplex}},
			},
			wantErrs: []string{CodeStatusMismatch},
		},

		// --- contradictions ----------------------------------------------------
		{
			name: "did in 5-10 October and not in October",
			rule: okRule([]Condition{
				dateEvent(EventPurchase, true, "2026-10-05", "2026-10-10"),
				dateEvent(EventPurchase, false, "2026-10-01", "2026-10-31"),
			}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:     "3 purchases in 30 days but none in 90",
			rule:     okRule([]Condition{countEvent(EventPurchase, 3, 30), event(EventPurchase, false, 90)}),
			wantErrs: []string{CodeContradiction},
		},
		{
			name:     "cart then no purchase, but no cart in 90 days",
			rule:     okRule([]Condition{seqEvent(EventAddToCart, 30, EventPurchase, false), event(EventAddToCart, false, 90)}),
			wantErrs: []string{CodeContradiction},
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

func TestValidateDoesNotModifyEventInput(t *testing.T) {
	in := okRule([]Condition{countEvent(EventPurchase, 1, 30), {
		Type: KindEvent, Event: EventAddToCart, Occurred: ptr(true), WithinDays: ptr(30),
		Then: &Then{Event: "PURCHASE", Occurred: ptr(false)},
	}})
	Validate(in)
	if in.Common[0].MinCount == nil || in.Common[1].Then.Event != "PURCHASE" {
		t.Fatalf("input was modified: %+v", in.Common)
	}
}
