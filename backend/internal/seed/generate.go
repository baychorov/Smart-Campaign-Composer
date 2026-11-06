// Package seed produces deterministic sample users and events and loads
// them into Postgres. Generation is pure; only store.go touches the DB.
package seed

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// Defaults used by cmd/seed and by tests that need the standard dataset.
const (
	DefaultSeed      uint64 = 42
	DefaultUserCount        = 5000
)

// Event types, matching the CHECK constraint in migrations.
const (
	EventSignup      = "signup"
	EventProductView = "product_view"
	EventAddToCart   = "add_to_cart"
	EventPurchase    = "purchase"
)

// Cities in canonical Turkish spelling with relative weights.
var cities = []struct {
	name   string
	weight int
}{
	{"İstanbul", 30}, {"Ankara", 14}, {"İzmir", 11}, {"Bursa", 7},
	{"Antalya", 7}, {"Adana", 5}, {"Konya", 5}, {"Gaziantep", 5},
	{"Eskişehir", 4}, {"Kayseri", 4}, {"Trabzon", 4}, {"Şanlıurfa", 4},
}

// Cities returns the canonical city names present in generated data.
func Cities() []string {
	out := make([]string, len(cities))
	for i, c := range cities {
		out[i] = c.name
	}
	return out
}

var firstNames = []string{
	"Ahmet", "Mehmet", "Mustafa", "Ali", "Hüseyin", "Hasan", "İbrahim", "Emre",
	"Burak", "Can", "Oğuz", "Murat", "Kerem", "Çağlar", "Barış", "Yusuf",
	"Ayşe", "Fatma", "Zeynep", "Elif", "Emine", "Hatice", "Merve", "Büşra",
	"Gül", "Şeyma", "Özge", "Irmak", "Defne", "Ece", "Selin", "İpek",
}

var lastNames = []string{
	"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk",
	"Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara",
	"Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Korkmaz", "Erdoğan", "Güneş",
}

// User mirrors a row of the users table.
type User struct {
	ID         int64
	FirstName  string
	LastName   string
	Email      string
	Phone      string
	City       string
	Age        int
	SMSOptIn   bool
	EmailOptIn bool
	CreatedAt  time.Time
}

// Event mirrors a row of the user_events table.
type Event struct {
	UserID     int64
	Type       string
	OccurredAt time.Time
}

// Dataset is everything the seed writes.
type Dataset struct {
	Users  []User
	Events []Event
}

const day = 24 * time.Hour

// Generate builds userCount random users plus the planted cohorts.
// The same (seed, userCount, now) always yields the same dataset, so tests
// can pin "now" and get stable audience counts.
func Generate(seed uint64, userCount int, now time.Time) Dataset {
	rng := rand.New(rand.NewPCG(seed, seed))
	now = now.UTC().Truncate(time.Second)

	var ds Dataset
	for i := 1; i <= userCount; i++ {
		u := randomUser(rng, int64(i), now)
		ds.Users = append(ds.Users, u)
		ds.Events = append(ds.Events, randomEvents(rng, u, now)...)
	}
	addCohorts(&ds, int64(userCount), now)
	return ds
}

func randomUser(rng *rand.Rand, id int64, now time.Time) User {
	return User{
		ID:         id,
		FirstName:  firstNames[rng.IntN(len(firstNames))],
		LastName:   lastNames[rng.IntN(len(lastNames))],
		Email:      fmt.Sprintf("user%05d@example.test", id),
		Phone:      fmt.Sprintf("+90555%07d", id),
		City:       pickCity(rng),
		Age:        randomAge(rng),
		SMSOptIn:   rng.IntN(100) < 70,
		EmailOptIn: rng.IntN(100) < 80,
		CreatedAt:  randomTimeBetween(rng, now.Add(-730*day), now.Add(-day)),
	}
}

func pickCity(rng *rand.Rand) string {
	total := 0
	for _, c := range cities {
		total += c.weight
	}
	n := rng.IntN(total)
	for _, c := range cities {
		if n < c.weight {
			return c.name
		}
		n -= c.weight
	}
	panic("unreachable: city weights")
}

// randomAge is mostly adults, with a few teenagers and seniors so that
// age filters have something to exclude at both ends.
func randomAge(rng *rand.Rand) int {
	switch p := rng.IntN(100); {
	case p < 4:
		return 13 + rng.IntN(5) // 13..17
	case p < 97:
		return 18 + rng.IntN(53) // 18..70
	default:
		return 71 + rng.IntN(20) // 71..90
	}
}

// randomEvents models a simple funnel: views, some views lead to a cart
// add, some cart adds lead to a purchase. All events fall in [signup, now).
func randomEvents(rng *rand.Rand, u User, now time.Time) []Event {
	events := []Event{{UserID: u.ID, Type: EventSignup, OccurredAt: u.CreatedAt}}

	activeFrom := u.CreatedAt
	if yearAgo := now.Add(-365 * day); activeFrom.Before(yearAgo) {
		activeFrom = yearAgo
	}

	for range rng.IntN(12) {
		view := randomTimeBetween(rng, activeFrom, now)
		events = append(events, Event{UserID: u.ID, Type: EventProductView, OccurredAt: view})

		if rng.IntN(100) >= 30 {
			continue
		}
		cart := view.Add(randomDuration(rng, 2*day))
		if !cart.Before(now) {
			continue
		}
		events = append(events, Event{UserID: u.ID, Type: EventAddToCart, OccurredAt: cart})

		if rng.IntN(100) >= 40 {
			continue
		}
		purchase := cart.Add(randomDuration(rng, 3*day))
		if !purchase.Before(now) {
			continue
		}
		events = append(events, Event{UserID: u.ID, Type: EventPurchase, OccurredAt: purchase})
	}
	return events
}

// randomTimeBetween returns a second-precision time in [from, to).
func randomTimeBetween(rng *rand.Rand, from, to time.Time) time.Time {
	return from.Add(randomDuration(rng, to.Sub(from))).Truncate(time.Second)
}

// randomDuration returns a duration in [1s, max).
func randomDuration(rng *rand.Rand, max time.Duration) time.Duration {
	return time.Second + time.Duration(rng.Int64N(int64(max-time.Second)))
}
