package seed

import (
	"fmt"
	"time"
)

// Cohort keys. Each cohort is one planted user whose expected audience
// membership is known in advance; tests find it via CohortEmail(key).
//
// The "case" cohorts target the case brief, interpreted per D-CART:
// city=İstanbul AND add_to_cart in last 30d AND NOT purchase in last 30d.
const (
	// In the case audience.
	CohortCaseTarget       = "case-target"        // cart 5d ago, no purchase
	CohortCaseOldPurchase  = "case-old-purchase"  // cart 5d, purchase 45d (outside window)
	CohortCaseWindowInside = "case-window-inside" // cart 29d ago
	CohortCaseEmailOnly    = "case-email-only"    // like target, email consent only
	CohortCaseNoConsent    = "case-no-consent"    // like target, no consent at all
	// Not in the case audience.
	CohortCasePurchaseThenCart = "case-purchase-then-cart" // D-CART edge: purchase 20d, cart 10d
	CohortCaseCartThenPurchase = "case-cart-then-purchase" // cart 20d, purchase 10d
	CohortCaseWindowOutside    = "case-window-outside"     // cart 31d ago
	CohortCaseOtherCity        = "case-other-city"         // Ankara, cart 5d

	// Age boundaries (İzmir, no events) for briefs like "25-34 yaş".
	CohortAge24 = "age-24"
	CohortAge25 = "age-25"
	CohortAge34 = "age-34"
	CohortAge35 = "age-35"
	// Age boundaries (Ankara) for "40 yaş üstü".
	CohortAge40 = "age-40"
	CohortAge41 = "age-41"

	// Personalisation edge cases (D-CONTENT).
	CohortNameEmpty = "name-empty" // first_name fallback
	CohortNameLong  = "name-long"  // longest first_name for worst-case render
)

// LongestFirstName is longer than every name in the random pool, so the
// worst-case render of {{first_name}} always uses it.
const LongestFirstName = "Muhammed Abdurrahman Şahabettin"

// CohortEmail returns the email address of a planted cohort user.
func CohortEmail(key string) string {
	return fmt.Sprintf("cohort.%s@example.test", key)
}

type relEvent struct {
	typ     string
	daysAgo int
}

type cohortSpec struct {
	key        string
	firstName  string // "Deneme" when empty, unless emptyName is set
	emptyName  bool
	city       string
	age        int
	sms, email bool
	events     []relEvent
}

func cohortSpecs() []cohortSpec {
	const ist = "İstanbul"
	cart := func(d int) relEvent { return relEvent{EventAddToCart, d} }
	buy := func(d int) relEvent { return relEvent{EventPurchase, d} }

	return []cohortSpec{
		{key: CohortCaseTarget, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(5)}},
		{key: CohortCaseOldPurchase, city: ist, age: 30, sms: true, email: true, events: []relEvent{buy(45), cart(5)}},
		{key: CohortCaseWindowInside, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(29)}},
		{key: CohortCaseEmailOnly, city: ist, age: 30, sms: false, email: true, events: []relEvent{cart(5)}},
		{key: CohortCaseNoConsent, city: ist, age: 30, sms: false, email: false, events: []relEvent{cart(5)}},
		{key: CohortCasePurchaseThenCart, city: ist, age: 30, sms: true, email: true, events: []relEvent{buy(20), cart(10)}},
		{key: CohortCaseCartThenPurchase, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(20), buy(10)}},
		{key: CohortCaseWindowOutside, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(31)}},
		{key: CohortCaseOtherCity, city: "Ankara", age: 30, sms: true, email: true, events: []relEvent{cart(5)}},

		{key: CohortAge24, city: "İzmir", age: 24, sms: true, email: true},
		{key: CohortAge25, city: "İzmir", age: 25, sms: true, email: true},
		{key: CohortAge34, city: "İzmir", age: 34, sms: true, email: true},
		{key: CohortAge35, city: "İzmir", age: 35, sms: true, email: true},
		{key: CohortAge40, city: "Ankara", age: 40, sms: true, email: true},
		{key: CohortAge41, city: "Ankara", age: 41, sms: true, email: true},

		{key: CohortNameEmpty, emptyName: true, city: ist, age: 30, sms: true, email: true},
		{key: CohortNameLong, firstName: LongestFirstName, city: ist, age: 30, sms: true, email: true},
	}
}

// addCohorts appends the planted users after the random ones. Every cohort
// user signed up 400 days ago so only the listed events fall in any window.
func addCohorts(ds *Dataset, lastID int64, now time.Time) {
	signup := now.Add(-400 * day)
	for i, s := range cohortSpecs() {
		id := lastID + int64(i) + 1
		first := s.firstName
		if first == "" && !s.emptyName {
			first = "Deneme"
		}
		ds.Users = append(ds.Users, User{
			ID:         id,
			FirstName:  first,
			LastName:   "Kohort",
			Email:      CohortEmail(s.key),
			Phone:      fmt.Sprintf("+90599%07d", id),
			City:       s.city,
			Age:        s.age,
			SMSOptIn:   s.sms,
			EmailOptIn: s.email,
			CreatedAt:  signup,
		})
		ds.Events = append(ds.Events, Event{UserID: id, Type: EventSignup, OccurredAt: signup})
		for _, e := range s.events {
			ds.Events = append(ds.Events, Event{UserID: id, Type: e.typ, OccurredAt: now.Add(-time.Duration(e.daysAgo) * day)})
		}
	}
}
