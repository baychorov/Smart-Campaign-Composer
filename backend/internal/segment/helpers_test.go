package segment

import (
	"math/rand/v2"
	"time"
)

// Shared test helpers. Files ending in _test.go are compiled only by
// `go test`, so none of this ends up in the binaries.

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func city(op Op, values ...string) Condition {
	return Condition{Type: KindAttribute, Field: FieldCity, Op: op, Values: values}
}

func age(op Op, v int) Condition {
	return Condition{Type: KindAttribute, Field: FieldAge, Op: op, Value: ptr(v)}
}

func event(ev EventType, occurred bool, days int) Condition {
	return Condition{Type: KindEvent, Event: ev, Occurred: ptr(occurred), WithinDays: ptr(days)}
}

func group(cs ...Condition) Group { return Group{Conditions: cs} }

func okRule(common []Condition, groups ...Group) Rule {
	return Rule{Status: StatusOK, AudienceScope: ScopeFiltered, Common: common, Groups: groups}
}

// caseRule is the case brief under D-CART: İstanbul, add_to_cart in the
// last 30 days, no purchase in the last 30 days.
func caseRule() Rule {
	return okRule([]Condition{
		city(OpIn, "İstanbul"),
		event(EventAddToCart, true, 30),
		event(EventPurchase, false, 30),
	})
}

// genCities mixes cities that exist in the seed with one that does not
// (Bolu), and spells some of them the way an LLM or a user might.
var genCities = []string{
	"İstanbul", "istanbul", "ISTANBUL", "Ankara", "İzmir", "IZMIR", "Bursa",
	"Antalya", "Konya", "Eskişehir", "eskisehir", "Şanlıurfa", "Trabzon", "Bolu",
}

var genDays = []int{1, 3, 7, 14, 29, 30, 31, 60, 90, 180, 365}

var genEvents = []EventType{EventSignup, EventProductView, EventAddToCart, EventPurchase}

// randomRule builds a structurally valid rule. It may still be
// contradictory or contain duplicate groups; callers skip those via
// Validate, which is itself under test.
func randomRule(rng *rand.Rand) Rule {
	var r Rule
	r.Status = StatusOK
	for range rng.IntN(4) {
		r.Common = append(r.Common, randomCondition(rng))
	}
	if rng.IntN(3) > 0 {
		for range 2 + rng.IntN(3) {
			var g Group
			for range 1 + rng.IntN(3) {
				g.Conditions = append(g.Conditions, randomCondition(rng))
			}
			r.Groups = append(r.Groups, g)
		}
	}
	r.AudienceScope = ScopeFiltered
	if len(r.Common) == 0 && len(r.Groups) == 0 {
		r.AudienceScope = ScopeAll
	}
	return r
}

func randomCondition(rng *rand.Rand) Condition {
	switch p := rng.IntN(100); {
	case p < 40:
		op := OpIn
		if rng.IntN(4) == 0 {
			op = OpNotIn
		}
		var vs []string
		for range 1 + rng.IntN(3) {
			vs = append(vs, genCities[rng.IntN(len(genCities))])
		}
		return city(op, vs...)
	case p < 65:
		op := OpGte
		if rng.IntN(2) == 0 {
			op = OpLte
		}
		return age(op, 16+rng.IntN(55))
	default:
		return event(genEvents[rng.IntN(len(genEvents))], rng.IntN(2) == 0, genDays[rng.IntN(len(genDays))])
	}
}
