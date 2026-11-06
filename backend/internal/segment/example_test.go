package segment_test

import (
	"fmt"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// The case brief: "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan
// İstanbul'daki kullanıcılar", as the LLM might return it (lower-case city).
// The SQL text holds only fixed identifiers and $n placeholders; the city
// name and event names appear only in the argument list.
func ExampleCompileAudience() {
	yes, no, days := true, false, 30
	rule := segment.Rule{
		Status:        segment.StatusOK,
		AudienceScope: segment.ScopeFiltered,
		Common: []segment.Condition{
			{Type: segment.KindAttribute, Field: segment.FieldCity, Op: segment.OpIn, Values: []string{"istanbul"}},
			{Type: segment.KindEvent, Event: segment.EventAddToCart, Occurred: &yes, WithinDays: &days},
			{Type: segment.KindEvent, Event: segment.EventPurchase, Occurred: &no, WithinDays: &days},
		},
	}

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	q, err := segment.CompileAudience(rule, segment.ChannelSMS, now)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(q.SQL)
	for i, a := range q.Args {
		if t, ok := a.(time.Time); ok {
			a = t.Format(time.DateTime)
		}
		fmt.Printf("$%d = %v\n", i+1, a)
	}
	fmt.Println(segment.Summarize(segment.Validate(rule).Rule))

	// Output:
	// SELECT u.id FROM users u WHERE u.sms_opt_in AND ((u.city = ANY($1)) AND (EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.event_type = $2 AND e.occurred_at >= $3)) AND (NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.event_type = $4 AND e.occurred_at >= $5)))
	// $1 = [İstanbul]
	// $2 = add_to_cart
	// $3 = 2026-10-07 12:00:00
	// $4 = purchase
	// $5 = 2026-10-07 12:00:00
	// Tüm koşullar: şehri İstanbul olan; son 30 günde sepete ürün eklemiş; son 30 günde alışveriş yapmamış
}
