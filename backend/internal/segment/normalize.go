package segment

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// normalizeEnums lower-cases every enum value with Turkish-aware folding.
// The API's schema guarantee does not cover enum case (D-LLM-OUT).
func (v *validator) normalizeEnums(r *Rule) {
	changed := false
	fold := func(s string) string {
		k := foldKey(s)
		if k != s {
			changed = true
		}
		return k
	}
	foldCond := func(c *Condition) {
		c.Type = Kind(fold(string(c.Type)))
		c.Field = Field(fold(string(c.Field)))
		c.Op = Op(fold(string(c.Op)))
		c.Event = EventType(fold(string(c.Event)))
		if c.Then != nil {
			c.Then.Event = EventType(fold(string(c.Then.Event)))
		}
	}

	r.Status = Status(fold(string(r.Status)))
	r.AudienceScope = AudienceScope(fold(string(r.AudienceScope)))
	for i := range r.Common {
		foldCond(&r.Common[i])
	}
	for g := range r.Groups {
		for i := range r.Groups[g].Conditions {
			foldCond(&r.Groups[g].Conditions[i])
		}
	}
	for i := range r.Unsupported {
		r.Unsupported[i].Reason = Reason(fold(string(r.Unsupported[i].Reason)))
	}
	if changed {
		v.warnf("", CodeNormalizedEnum, "enum değerleri küçük harfe çevrildi")
	}
}

// normalizeCities rewrites city names to canonical spelling and removes
// duplicates. Unknown names are errors.
func (v *validator) normalizeCities(path string, c *Condition) {
	seen := map[string]bool{}
	out := make([]string, 0, len(c.Values))
	for i, raw := range c.Values {
		city, ok := CanonicalCity(raw)
		if !ok {
			v.errorf(fmt.Sprintf("%s.values[%d]", path, i), CodeUnknownCity, "bilinmeyen şehir: %q", raw)
			continue
		}
		if city != raw {
			v.warnf(fmt.Sprintf("%s.values[%d]", path, i), CodeNormalizedCity, "%q, %q olarak düzeltildi", raw, city)
		}
		if seen[city] {
			v.warnf(fmt.Sprintf("%s.values[%d]", path, i), CodeDuplicateValue, "%q birden fazla kez yazılmış", city)
			continue
		}
		seen[city] = true
		out = append(out, city)
	}
	c.Values = out
	if len(out) > MaxInValues {
		v.errorf(path+".values", CodeTooComplex, "en fazla %d değer olabilir, %d geldi", MaxInValues, len(out))
	}
}

// normalizeStructure applies meaning-preserving rewrites:
//   - duplicate conditions inside common or a group are dropped
//   - identical groups are an error (D-SEG)
//   - a group that contains every condition of another group is dropped,
//     because A ∨ (A ∧ B) = A
//   - a single remaining group is merged into common (canonical form)
func (v *validator) normalizeStructure(r *Rule) {
	r.Common = v.dedupeConditions("common", r.Common)
	for g := range r.Groups {
		r.Groups[g].Conditions = v.dedupeConditions(fmt.Sprintf("groups[%d]", g), r.Groups[g].Conditions)
	}

	keys := make([]map[string]bool, len(r.Groups))
	for g, grp := range r.Groups {
		keys[g] = map[string]bool{}
		for _, c := range grp.Conditions {
			keys[g][conditionKey(c)] = true
		}
	}

	for a := range r.Groups {
		for b := a + 1; b < len(r.Groups); b++ {
			if sameKeys(keys[a], keys[b]) {
				v.errorf(fmt.Sprintf("groups[%d]", b), CodeDuplicateGroup, "%d. grup %d. grupla aynı", b+1, a+1)
			}
		}
	}
	if !v.res.Valid() {
		return
	}

	var kept []Group
	newIndex := make([]int, len(r.Groups)) // old group index -> new, -1 if gone
	for b, grp := range r.Groups {
		coveredBy := -1
		for a := range r.Groups {
			if a != b && isSubset(keys[a], keys[b]) {
				coveredBy = a
				break
			}
		}
		if coveredBy >= 0 {
			v.warnf(fmt.Sprintf("groups[%d]", b), CodeCoveredGroup,
				"%d. grup %d. grubun içinde kaldığı için çıkarıldı; kitle değişmez", b+1, coveredBy+1)
			newIndex[b] = -1
			continue
		}
		newIndex[b] = len(kept)
		kept = append(kept, grp)
	}
	r.Groups = kept

	if len(r.Groups) == 1 {
		v.warnf("groups", CodeSingleGroup, "tek grup ortak koşullara taşındı")
		r.Common = v.dedupeConditions("common", append(r.Common, r.Groups[0].Conditions...))
		r.Groups = nil
		for i := range newIndex {
			newIndex[i] = -1
		}
	}

	// Keep unsupported[].group pointing at the same group after the rewrite;
	// a group that was dropped or merged no longer has an index.
	for i, u := range r.Unsupported {
		if u.Group == nil {
			continue
		}
		if n := newIndex[*u.Group]; n >= 0 {
			r.Unsupported[i].Group = &n
		} else {
			r.Unsupported[i].Group = nil
		}
	}
}

func (v *validator) dedupeConditions(path string, cs []Condition) []Condition {
	seen := map[string]bool{}
	var out []Condition
	for i, c := range cs {
		k := conditionKey(c)
		if seen[k] {
			v.warnf(fmt.Sprintf("%s[%d]", path, i), CodeDuplicateCondition, "aynı koşul tekrar yazılmış")
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

// conditionKey identifies a condition by meaning; the order of "in" values
// does not matter.
func conditionKey(c Condition) string {
	values := slices.Clone(c.Values)
	slices.Sort(values)
	parts := []string{string(c.Type), string(c.Field), string(c.Op), strings.Join(values, "\x1f"), string(c.Event)}
	parts = append(parts, c.From, c.To)
	for _, p := range []*int{c.Value, c.WithinDays, c.MinCount} {
		if p == nil {
			parts = append(parts, "-")
		} else {
			parts = append(parts, strconv.Itoa(*p))
		}
	}
	if c.Occurred != nil {
		parts = append(parts, strconv.FormatBool(*c.Occurred))
	}
	// Every field that changes the meaning must be in the key: a key that
	// ignored MinCount would treat "bought" and "bought 3 times" as the
	// same condition and drop one of them, widening the audience.
	if c.Then != nil && c.Then.Occurred != nil {
		parts = append(parts, "then", string(c.Then.Event), strconv.FormatBool(*c.Then.Occurred))
	}
	return strings.Join(parts, "|")
}

func sameKeys(a, b map[string]bool) bool {
	return len(a) == len(b) && isSubset(a, b)
}

// isSubset reports whether every key of a is in b.
func isSubset(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
