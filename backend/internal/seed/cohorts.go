package seed

import (
	"fmt"
	"time"
)

// Cohort keys. Each cohort is one planted user whose expected audience
// membership is known in advance; tests find it via CohortEmail(key).
//
// The "case" cohorts target the case brief. Two readings are tested:
//   - sequence (current, PLAN step 14b): İstanbul AND add_to_cart in the
//     last 30d with no purchase after it;
//   - independent (D-CART): İstanbul AND add_to_cart in the last 30d AND
//     no purchase in the last 30d.
//
// They differ only on CohortCasePurchaseThenCart.
const (
	// In the case audience (both readings).
	CohortCaseTarget       = "case-target"        // cart 5d ago, no purchase
	CohortCaseOldPurchase  = "case-old-purchase"  // cart 5d, purchase 45d (outside window)
	CohortCaseWindowInside = "case-window-inside" // cart 29d ago
	CohortCaseEmailOnly    = "case-email-only"    // like target, email consent only
	CohortCaseNoConsent    = "case-no-consent"    // like target, no consent at all
	// In the sequence reading only: the cart add came after the purchase.
	CohortCasePurchaseThenCart = "case-purchase-then-cart" // purchase 20d, cart 10d
	// In the sequence reading only: the last cart add has no purchase after it.
	CohortCaseCartBuyCart = "case-cart-buy-cart" // cart 20d, purchase 15d, cart 5d
	// Not in the case audience.
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

	// Purchase counts (İzmir, age 30) for "at least 3 purchases in the
	// last 90 days".
	CohortBuys2     = "buys-2"      // purchases 10d, 50d
	CohortBuys3     = "buys-3"      // purchases 10d, 50d, 89d
	CohortBuys3Late = "buys-3-late" // purchases 10d, 50d, 91d: only 2 in 90d

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
		{key: CohortCaseCartBuyCart, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(20), buy(15), cart(5)}},
		{key: CohortCaseCartThenPurchase, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(20), buy(10)}},
		{key: CohortCaseWindowOutside, city: ist, age: 30, sms: true, email: true, events: []relEvent{cart(31)}},
		{key: CohortCaseOtherCity, city: "Ankara", age: 30, sms: true, email: true, events: []relEvent{cart(5)}},

		{key: CohortAge24, city: "İzmir", age: 24, sms: true, email: true},
		{key: CohortAge25, city: "İzmir", age: 25, sms: true, email: true},
		{key: CohortAge34, city: "İzmir", age: 34, sms: true, email: true},
		{key: CohortAge35, city: "İzmir", age: 35, sms: true, email: true},
		{key: CohortAge40, city: "Ankara", age: 40, sms: true, email: true},
		{key: CohortAge41, city: "Ankara", age: 41, sms: true, email: true},

		{key: CohortBuys2, city: "İzmir", age: 30, sms: true, email: true, events: []relEvent{buy(50), buy(10)}},
		{key: CohortBuys3, city: "İzmir", age: 30, sms: true, email: true, events: []relEvent{buy(89), buy(50), buy(10)}},
		{key: CohortBuys3Late, city: "İzmir", age: 30, sms: true, email: true, events: []relEvent{buy(91), buy(50), buy(10)}},

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
