package seed

import (
	"reflect"
	"slices"
	"testing"
	"time"
	"unicode/utf8"
)

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func TestGenerateIsDeterministic(t *testing.T) {
	a := Generate(DefaultSeed, 500, testNow)
	b := Generate(DefaultSeed, 500, testNow)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed and now produced different datasets")
	}

	c := Generate(DefaultSeed+1, 500, testNow)
	if reflect.DeepEqual(a.Users, c.Users) {
		t.Fatal("different seeds produced identical users")
	}
}

func TestGenerateInvariants(t *testing.T) {
	const count = 2000
	ds := Generate(DefaultSeed, count, testNow)

	if want := count + len(cohortSpecs()); len(ds.Users) != want {
		t.Fatalf("got %d users, want %d", len(ds.Users), want)
	}

	validCities := Cities()
	emails := map[string]bool{}
	phones := map[string]bool{}
	ids := map[int64]User{}
	for _, u := range ds.Users {
		if emails[u.Email] {
			t.Errorf("duplicate email %s", u.Email)
		}
		if phones[u.Phone] {
			t.Errorf("duplicate phone for user %d", u.ID)
		}
		if _, dup := ids[u.ID]; dup {
			t.Errorf("duplicate id %d", u.ID)
		}
		emails[u.Email], phones[u.Phone], ids[u.ID] = true, true, u

		if u.Age < 13 || u.Age > 100 {
			t.Errorf("user %d: age %d outside 13..100", u.ID, u.Age)
		}
		if !slices.Contains(validCities, u.City) {
			t.Errorf("user %d: unknown city %q", u.ID, u.City)
		}
		if !u.CreatedAt.Before(testNow) {
			t.Errorf("user %d: created in the future", u.ID)
		}
	}

	validTypes := []string{EventSignup, EventProductView, EventAddToCart, EventPurchase}
	signups := map[int64]int{}
	for _, e := range ds.Events {
		u, ok := ids[e.UserID]
		if !ok {
			t.Fatalf("event for unknown user %d", e.UserID)
		}
		if !slices.Contains(validTypes, e.Type) {
			t.Errorf("unknown event type %q", e.Type)
		}
		if e.OccurredAt.Before(u.CreatedAt) || !e.OccurredAt.Before(testNow) {
			t.Errorf("user %d: %s at %v outside [signup, now)", u.ID, e.Type, e.OccurredAt)
		}
		if e.Type == EventSignup {
			signups[e.UserID]++
		}
	}
	for id := range ids {
		if signups[id] != 1 {
			t.Errorf("user %d has %d signup events, want 1", id, signups[id])
		}
	}

	// The random data must exercise every filter, not just the cohorts.
	for _, typ := range validTypes {
		if !slices.ContainsFunc(ds.Events, func(e Event) bool { return e.Type == typ && e.UserID <= count }) {
			t.Errorf("random users have no %s events", typ)
		}
	}
	for _, city := range validCities {
		if !slices.ContainsFunc(ds.Users, func(u User) bool { return u.City == city && u.ID <= count }) {
			t.Errorf("random users have nobody in %s", city)
		}
	}
}

func TestCohorts(t *testing.T) {
	ds := Generate(DefaultSeed, 100, testNow)

	byEmail := map[string]User{}
	for _, u := range ds.Users {
		byEmail[u.Email] = u
	}
	eventsOf := func(id int64, typ string) []time.Time {
		var out []time.Time
		for _, e := range ds.Events {
			if e.UserID == id && e.Type == typ {
				out = append(out, e.OccurredAt)
			}
		}
		return out
	}

	for _, s := range cohortSpecs() {
		t.Run(s.key, func(t *testing.T) {
			u, ok := byEmail[CohortEmail(s.key)]
			if !ok {
				t.Fatal("cohort user missing")
			}
			if u.City != s.city || u.Age != s.age || u.SMSOptIn != s.sms || u.EmailOptIn != s.email {
				t.Errorf("profile mismatch: %+v", u)
			}
			for _, e := range s.events {
				want := testNow.Add(-time.Duration(e.daysAgo) * day)
				if !slices.Contains(eventsOf(u.ID, e.typ), want) {
					t.Errorf("missing %s %d days ago", e.typ, e.daysAgo)
				}
			}
		})
	}

	// The D-CART edge case: purchase before cart inside the window.
	edge := byEmail[CohortEmail(CohortCasePurchaseThenCart)]
	buy, cart := eventsOf(edge.ID, EventPurchase), eventsOf(edge.ID, EventAddToCart)
	if len(buy) != 1 || len(cart) != 1 || !buy[0].Before(cart[0]) {
		t.Errorf("D-CART cohort must purchase before adding to cart, got purchase=%v cart=%v", buy, cart)
	}
}

func TestNameEdgeCases(t *testing.T) {
	ds := Generate(DefaultSeed, DefaultUserCount, testNow)

	var empty, longest string
	var maxRunes int
	for _, u := range ds.Users {
		if u.FirstName == "" {
			empty = u.Email
		}
		// Rune count, not len(): "Ş" is 2 bytes but 1 character.
		if n := utf8.RuneCountInString(u.FirstName); n > maxRunes {
			maxRunes, longest = n, u.Email
		}
	}
	if empty != CohortEmail(CohortNameEmpty) {
		t.Errorf("only the name-empty cohort should have an empty first name, got %q", empty)
	}
	if longest != CohortEmail(CohortNameLong) {
		t.Errorf("name-long cohort must hold the longest first name, got %q", longest)
	}
}
