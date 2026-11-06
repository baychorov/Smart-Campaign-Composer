package segment

import (
	"fmt"
	"slices"
)

// Issue codes. Errors block approval; warnings record an automatic fix or
// something the marketer should know.
const (
	// Errors.
	CodeInvalidEnum      = "invalid_enum"
	CodeInvalidCondition = "invalid_condition"
	CodeUnknownCity      = "unknown_city"
	CodeOutOfRange       = "out_of_range"
	CodeTooComplex       = "too_complex"
	CodeDuplicateGroup   = "duplicate_group"
	CodeContradiction    = "contradiction"
	CodeStatusMismatch   = "status_mismatch"
	CodeScopeMismatch    = "scope_mismatch"
	CodeEmptyRule        = "empty_rule"
	CodeInvalidUnsupport = "invalid_unsupported"

	// Warnings (automatic, meaning-preserving fixes).
	CodeNormalizedEnum     = "normalized_enum"
	CodeNormalizedCity     = "normalized_city"
	CodeDuplicateValue     = "duplicate_value"
	CodeDuplicateCondition = "duplicate_condition"
	CodeCoveredGroup       = "covered_group"
	CodeSingleGroup        = "single_group"
)

// Issue is one validation finding. Message is Turkish because it is shown
// to the marketer and sent back to the LLM in the repair prompt.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is the outcome of Validate. Rule is the normalised rule; use it,
// not the input, for compiling and display.
type Result struct {
	Rule     Rule
	Errors   []Issue
	Warnings []Issue
}

// Valid reports whether the rule has no errors.
func (r Result) Valid() bool { return len(r.Errors) == 0 }

// Approvable reports whether the rule is valid and describes an audience
// (status ok or partial). A partial rule still needs explicit consent from
// the marketer (D-PARTIAL); that is the campaign's job.
func (r Result) Approvable() bool {
	return r.Valid() && (r.Rule.Status == StatusOK || r.Rule.Status == StatusPartial)
}

type validator struct {
	res Result
}

func (v *validator) errorf(path, code, format string, args ...any) {
	v.res.Errors = append(v.res.Errors, Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) warnf(path, code, format string, args ...any) {
	v.res.Warnings = append(v.res.Warnings, Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

// Validate normalises the rule and checks it against D-SEG. The input is not
// modified. Steps:
//  1. enum case, condition shape, ranges, city names, limits
//  2. meaning-preserving normalisation (dedupe, covered groups, single group)
//  3. contradictions and status/scope consistency
//
// Stage 2 and 3 run only if stage 1 found no errors, since they assume a
// well-formed rule.
func Validate(in Rule) Result {
	v := &validator{res: Result{Rule: cloneRule(in)}}
	r := &v.res.Rule

	v.normalizeEnums(r)
	v.checkTopLevel(r)
	for i := range r.Common {
		v.checkCondition(fmt.Sprintf("common[%d]", i), &r.Common[i])
	}
	for g := range r.Groups {
		for i := range r.Groups[g].Conditions {
			v.checkCondition(fmt.Sprintf("groups[%d].conditions[%d]", g, i), &r.Groups[g].Conditions[i])
		}
	}
	v.checkLimits(r)
	v.checkUnsupported(r)
	if !v.res.Valid() {
		return v.res
	}

	v.normalizeStructure(r)
	v.checkContradictions(r)
	v.checkStatus(r)
	return v.res
}

func (v *validator) checkTopLevel(r *Rule) {
	switch r.Status {
	case StatusOK, StatusPartial, StatusUnsupported, StatusRejected:
	default:
		v.errorf("status", CodeInvalidEnum, "geçersiz status: %q", r.Status)
	}
	switch r.AudienceScope {
	case ScopeFiltered, ScopeAll:
	default:
		v.errorf("audience_scope", CodeInvalidEnum, "geçersiz audience_scope: %q", r.AudienceScope)
	}
}

func (v *validator) checkCondition(path string, c *Condition) {
	switch c.Type {
	case KindAttribute:
		v.checkAttribute(path, c)
	case KindEvent:
		v.checkEvent(path, c)
	default:
		v.errorf(path+".type", CodeInvalidEnum, "geçersiz koşul tipi: %q", c.Type)
	}
}

func (v *validator) checkAttribute(path string, c *Condition) {
	if c.Event != "" || c.Occurred != nil || c.WithinDays != nil {
		v.errorf(path, CodeInvalidCondition, "attribute koşulunda olay alanları bulunamaz")
	}
	switch c.Field {
	case FieldCity:
		if c.Op != OpIn && c.Op != OpNotIn {
			v.errorf(path+".op", CodeInvalidCondition, "city için op in veya not_in olmalı, %q geldi", c.Op)
		}
		if c.Value != nil {
			v.errorf(path+".value", CodeInvalidCondition, "city için value değil values kullanılır")
		}
		if len(c.Values) == 0 {
			v.errorf(path+".values", CodeInvalidCondition, "city için en az bir değer gerekli")
		}
		v.normalizeCities(path, c)
	case FieldAge:
		if c.Op != OpGte && c.Op != OpLte {
			v.errorf(path+".op", CodeInvalidCondition, "age için op gte veya lte olmalı, %q geldi", c.Op)
		}
		if len(c.Values) > 0 {
			v.errorf(path+".values", CodeInvalidCondition, "age için values değil value kullanılır")
		}
		if c.Value == nil {
			v.errorf(path+".value", CodeInvalidCondition, "age için value gerekli")
		} else if *c.Value < MinAge || *c.Value > MaxAge {
			v.errorf(path+".value", CodeOutOfRange, "age %d-%d aralığında olmalı, %d geldi", MinAge, MaxAge, *c.Value)
		}
	default:
		v.errorf(path+".field", CodeInvalidEnum, "bilinmeyen alan: %q", c.Field)
	}
}

func (v *validator) checkEvent(path string, c *Condition) {
	if c.Field != "" || c.Op != "" || len(c.Values) > 0 || c.Value != nil {
		v.errorf(path, CodeInvalidCondition, "event koşulunda attribute alanları bulunamaz")
	}
	switch c.Event {
	case EventSignup, EventProductView, EventAddToCart, EventPurchase:
	default:
		v.errorf(path+".event", CodeInvalidEnum, "bilinmeyen olay: %q", c.Event)
	}
	if c.Occurred == nil {
		v.errorf(path+".occurred", CodeInvalidCondition, "occurred gerekli")
	}
	if c.WithinDays == nil {
		v.errorf(path+".within_days", CodeInvalidCondition, "within_days gerekli")
	} else if *c.WithinDays < MinWithinDays || *c.WithinDays > MaxWithinDays {
		v.errorf(path+".within_days", CodeOutOfRange, "within_days %d-%d aralığında olmalı, %d geldi", MinWithinDays, MaxWithinDays, *c.WithinDays)
	}
}

func (v *validator) checkLimits(r *Rule) {
	total := len(r.Common)
	if len(r.Groups) > MaxGroups {
		v.errorf("groups", CodeTooComplex, "en fazla %d grup olabilir, %d geldi", MaxGroups, len(r.Groups))
	}
	if len(r.Common) > MaxCommonConds {
		v.errorf("common", CodeTooComplex, "common içinde en fazla %d koşul olabilir, %d geldi", MaxCommonConds, len(r.Common))
	}
	for g, grp := range r.Groups {
		total += len(grp.Conditions)
		if len(grp.Conditions) > MaxGroupConditions {
			v.errorf(fmt.Sprintf("groups[%d]", g), CodeTooComplex, "grup başına en fazla %d koşul olabilir, %d geldi", MaxGroupConditions, len(grp.Conditions))
		}
		if len(grp.Conditions) == 0 {
			v.errorf(fmt.Sprintf("groups[%d]", g), CodeInvalidCondition, "grup boş olamaz")
		}
	}
	if total > MaxTotalConditions {
		v.errorf("", CodeTooComplex, "toplam en fazla %d koşul olabilir, %d geldi", MaxTotalConditions, total)
	}
}

func (v *validator) checkUnsupported(r *Rule) {
	for i, u := range r.Unsupported {
		path := fmt.Sprintf("unsupported[%d]", i)
		switch u.Reason {
		case ReasonSequence, ReasonAggregate, ReasonRanking, ReasonUnknownField, ReasonAbsoluteDate,
			ReasonScheduling, ReasonContradiction, ReasonTooComplex, ReasonOffTopic:
		default:
			v.errorf(path+".reason", CodeInvalidEnum, "geçersiz reason: %q", u.Reason)
		}
		if u.Text == "" {
			v.errorf(path+".text", CodeInvalidUnsupport, "text boş olamaz")
		}
		if u.Group != nil && (*u.Group < 0 || *u.Group >= len(r.Groups)) {
			v.errorf(path+".group", CodeInvalidUnsupport, "group %d geçersiz; %d grup var", *u.Group, len(r.Groups))
		}
	}
}

// checkContradictions looks for conjunctions that no user can satisfy.
// Each group is checked together with common, since that is what runs.
func (v *validator) checkContradictions(r *Rule) {
	if len(r.Groups) == 0 {
		if msg := contradiction(r.Common); msg != "" {
			v.errorf("common", CodeContradiction, "%s", msg)
		}
		return
	}
	for g, grp := range r.Groups {
		if msg := contradiction(slices.Concat(r.Common, grp.Conditions)); msg != "" {
			v.errorf(fmt.Sprintf("groups[%d]", g), CodeContradiction, "%s (ortak koşullarla birlikte)", msg)
		}
	}
}

// contradiction returns a description if the AND of conds is unsatisfiable.
func contradiction(conds []Condition) string {
	minAge, maxAge := MinAge, MaxAge
	var allowed map[string]bool // nil: no "in" seen yet
	excluded := map[string]bool{}
	did := map[EventType]int{}    // smallest window with Occurred=true
	didNot := map[EventType]int{} // largest window with Occurred=false

	for _, c := range conds {
		switch {
		case c.Field == FieldAge && c.Op == OpGte:
			minAge = max(minAge, *c.Value)
		case c.Field == FieldAge && c.Op == OpLte:
			maxAge = min(maxAge, *c.Value)
		case c.Field == FieldCity && c.Op == OpIn:
			next := map[string]bool{}
			for _, city := range c.Values {
				if allowed == nil || allowed[city] {
					next[city] = true
				}
			}
			allowed = next
		case c.Field == FieldCity && c.Op == OpNotIn:
			for _, city := range c.Values {
				excluded[city] = true
			}
		case c.Type == KindEvent && *c.Occurred:
			if d, ok := did[c.Event]; !ok || *c.WithinDays < d {
				did[c.Event] = *c.WithinDays
			}
		case c.Type == KindEvent && !*c.Occurred:
			didNot[c.Event] = max(didNot[c.Event], *c.WithinDays)
		}
	}

	if minAge > maxAge {
		return fmt.Sprintf("yaş en az %d ve en fazla %d olamaz", minAge, maxAge)
	}
	if allowed != nil {
		possible := false
		for city := range allowed {
			if !excluded[city] {
				possible = true
				break
			}
		}
		if !possible {
			return "şehir koşulları birbirini dışlıyor"
		}
	}
	// "did X in last N days" and "did not do X in last M days" with M >= N:
	// the N-day window lies inside the M-day window.
	for ev, n := range did {
		if m, ok := didNot[ev]; ok && m >= n {
			return fmt.Sprintf("%s olayı son %d günde hem var hem yok", ev, n)
		}
	}
	return ""
}

// checkStatus enforces consistency between status, scope and the lists.
func (v *validator) checkStatus(r *Rule) {
	hasConds := len(r.Common) > 0 || len(r.Groups) > 0
	hasUnsupported := len(r.Unsupported) > 0

	switch r.Status {
	case StatusOK, StatusPartial:
		if r.Status == StatusOK && hasUnsupported {
			v.errorf("status", CodeStatusMismatch, "status ok iken unsupported listesi boş olmalı")
		}
		if r.Status == StatusPartial && !hasUnsupported {
			v.errorf("status", CodeStatusMismatch, "status partial iken unsupported listesi dolu olmalı")
		}
		if r.AudienceScope == ScopeAll && hasConds {
			v.errorf("audience_scope", CodeScopeMismatch, "audience_scope all iken koşul olamaz")
		}
		if r.AudienceScope == ScopeFiltered && !hasConds {
			v.errorf("audience_scope", CodeEmptyRule, "audience_scope filtered iken en az bir koşul gerekli")
		}
	case StatusUnsupported, StatusRejected:
		if hasConds {
			v.errorf("status", CodeStatusMismatch, "status %s iken koşul olamaz", r.Status)
		}
		if r.Status == StatusUnsupported && !hasUnsupported {
			v.errorf("status", CodeStatusMismatch, "status unsupported iken unsupported listesi dolu olmalı")
		}
	}
}

func cloneRule(r Rule) Rule {
	out := r
	out.Common = cloneConditions(r.Common)
	out.Groups = slices.Clone(r.Groups)
	for i, g := range out.Groups {
		out.Groups[i] = Group{Conditions: cloneConditions(g.Conditions)}
	}
	out.Assumptions = slices.Clone(r.Assumptions)
	out.Notes = slices.Clone(r.Notes)
	out.Unsupported = slices.Clone(r.Unsupported)
	for i, u := range out.Unsupported {
		out.Unsupported[i].Group = clonePtr(u.Group)
	}
	return out
}

// cloneConditions deep-copies conditions; nil stays nil.
func cloneConditions(cs []Condition) []Condition {
	out := slices.Clone(cs)
	for i, c := range out {
		out[i] = c
		out[i].Values = slices.Clone(c.Values)
		out[i].Value = clonePtr(c.Value)
		out[i].Occurred = clonePtr(c.Occurred)
		out[i].WithinDays = clonePtr(c.WithinDays)
	}
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
