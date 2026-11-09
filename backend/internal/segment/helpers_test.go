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

func countEvent(ev EventType, n, days int) Condition {
	c := event(ev, true, days)
	c.MinCount = ptr(n)
	return c
}

func dateEvent(ev EventType, occurred bool, from, to string) Condition {
	return Condition{Type: KindEvent, Event: ev, Occurred: ptr(occurred), From: from, To: to}
}

func seqEvent(first EventType, days int, then EventType, thenOccurred bool) Condition {
	c := event(first, true, days)
	c.Then = &Then{Event: then, Occurred: ptr(thenOccurred)}
	return c
}

// caseRule is the case brief as read since PLAN step 14b: İstanbul, and an
// add_to_cart in the last 30 days with no purchase after it.
func caseRule() Rule {
	return okRule([]Condition{
		city(OpIn, "İstanbul"),
		seqEvent(EventAddToCart, 30, EventPurchase, false),
	})
}

// cartNoPurchaseRule is the earlier reading of the case brief (D-CART):
// add_to_cart in the last 30 days and no purchase in the last 30 days, as
// two independent conditions.
func cartNoPurchaseRule() Rule {
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
		return randomEvent(rng)
	}
}

// randomEvent covers every event form: plain, count, calendar range and
// sequence. Ranges end before or on testNow's day and start at most 200
// days earlier, so they overlap the seed's events.
func randomEvent(rng *rand.Rand) Condition {
	ev := genEvents[rng.IntN(len(genEvents))]
	c := event(ev, rng.IntN(2) == 0, genDays[rng.IntN(len(genDays))])
	if rng.IntN(3) == 0 {
		end := testNow.AddDate(0, 0, -rng.IntN(120))
		start := end.AddDate(0, 0, -rng.IntN(80))
		c.WithinDays = nil
		c.From, c.To = start.Format(DateLayout), end.Format(DateLayout)
	}
	if !*c.Occurred {
		return c
	}
	switch rng.IntN(4) {
	case 0:
		if ev != EventSignup {
			c.MinCount = ptr(2 + rng.IntN(3))
		}
	case 1:
		then := genEvents[rng.IntN(len(genEvents))]
		if then != ev {
			c.Then = &Then{Event: then, Occurred: ptr(rng.IntN(2) == 0)}
		}
	}
	return c
}
