package segment

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const existsEvent = "EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.event_type = "

func TestCompileAudience(t *testing.T) {
	daysAgo := func(n int) time.Time { return testNow.Add(-time.Duration(n) * 24 * time.Hour) }

	tests := []struct {
		name     string
		rule     Rule
		channel  Channel
		wantSQL  string
		wantArgs []any
	}{
		{
			name:    "case brief as a sequence, SMS",
			rule:    caseRule(),
			channel: ChannelSMS,
			wantSQL: "SELECT u.id FROM users u WHERE u.sms_opt_in AND (" +
				"(u.city = ANY($1)) AND " +
				"(" + existsEvent + "$2 AND e.occurred_at >= $3 AND " +
				"NOT EXISTS (SELECT 1 FROM user_events f WHERE f.user_id = u.id AND f.event_type = $4 AND f.occurred_at > e.occurred_at))))",
			wantArgs: []any{[]string{"İstanbul"}, "add_to_cart", daysAgo(30), "purchase"},
		},
		{
			name:    "then occurred",
			rule:    okRule([]Condition{seqEvent(EventProductView, 7, EventAddToCart, true)}),
			channel: ChannelEmail,
			wantSQL: "SELECT u.id FROM users u WHERE u.email_opt_in AND (" +
				"(" + existsEvent + "$1 AND e.occurred_at >= $2 AND " +
				"EXISTS (SELECT 1 FROM user_events f WHERE f.user_id = u.id AND f.event_type = $3 AND f.occurred_at > e.occurred_at))))",
			wantArgs: []any{"product_view", daysAgo(7), "add_to_cart"},
		},
		{
			name:    "at least 3 purchases in 90 days",
			rule:    okRule([]Condition{countEvent(EventPurchase, 3, 90)}),
			channel: ChannelSMS,
			wantSQL: "SELECT u.id FROM users u WHERE u.sms_opt_in AND (" +
				"((SELECT count(*) FROM user_events e WHERE e.user_id = u.id AND e.event_type = $1 AND e.occurred_at >= $2) >= $3))",
			wantArgs: []any{"purchase", daysAgo(90), 3},
		},
		{
			// Calendar days are Istanbul days: 1 October 00:00 +03:00 up to,
			// not including, 16 October 00:00 +03:00.
			name:    "calendar range, did not",
			rule:    okRule([]Condition{dateEvent(EventProductView, false, "2026-10-01", "2026-10-15")}),
			channel: ChannelSMS,
			wantSQL: "SELECT u.id FROM users u WHERE u.sms_opt_in AND (" +
				"(NOT " + existsEvent + "$1 AND e.occurred_at >= $2 AND e.occurred_at < $3)))",
			wantArgs: []any{"product_view",
				time.Date(2026, 10, 1, 0, 0, 0, 0, Istanbul), time.Date(2026, 10, 16, 0, 0, 0, 0, Istanbul)},
		},
		{
			name:    "independent cart and purchase (D-CART), SMS",
			rule:    cartNoPurchaseRule(),
			channel: ChannelSMS,
			wantSQL: "SELECT u.id FROM users u WHERE u.sms_opt_in AND (" +
				"(u.city = ANY($1)) AND " +
				"(" + existsEvent + "$2 AND e.occurred_at >= $3)) AND " +
				"(NOT " + existsEvent + "$4 AND e.occurred_at >= $5)))",
			wantArgs: []any{[]string{"İstanbul"}, "add_to_cart", daysAgo(30), "purchase", daysAgo(30)},
		},
		{
			name: "common and two groups (brief 17), email",
			rule: okRule([]Condition{event(EventAddToCart, true, 30)},
				group(city(OpIn, "İstanbul"), age(OpLte, 24)),
				group(city(OpIn, "Ankara"), age(OpGte, 41))),
			channel: ChannelEmail,
			// consent AND (common AND ((g1) OR (g2)))
			wantSQL: "SELECT u.id FROM users u WHERE u.email_opt_in AND (" +
				"(" + existsEvent + "$1 AND e.occurred_at >= $2)) AND " +
				"(((u.city = ANY($3)) AND (u.age <= $4)) OR ((u.city = ANY($5)) AND (u.age >= $6))))",
			wantArgs: []any{"add_to_cart", daysAgo(30), []string{"İstanbul"}, 24, []string{"Ankara"}, 41},
		},
		{
			name:     "groups only, not_in",
			rule:     okRule(nil, group(city(OpNotIn, "İstanbul")), group(age(OpGte, 65))),
			channel:  ChannelSMS,
			wantSQL:  "SELECT u.id FROM users u WHERE u.sms_opt_in AND ((((u.city <> ALL($1))) OR ((u.age >= $2))))",
			wantArgs: []any{[]string{"İstanbul"}, 65},
		},
		{
			name:     "all users still filtered by consent",
			rule:     Rule{Status: StatusOK, AudienceScope: ScopeAll},
			channel:  ChannelEmail,
			wantSQL:  "SELECT u.id FROM users u WHERE u.email_opt_in AND (TRUE)",
			wantArgs: nil,
		},
		{
			name:     "compiles the normalised rule",
			rule:     okRule([]Condition{city(OpIn, "IZMIR")}),
			channel:  ChannelSMS,
			wantSQL:  "SELECT u.id FROM users u WHERE u.sms_opt_in AND ((u.city = ANY($1)))",
			wantArgs: []any{[]string{"İzmir"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := CompileAudience(tt.rule, tt.channel, testNow)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if q.SQL != tt.wantSQL {
				t.Errorf("SQL\n got: %s\nwant: %s", q.SQL, tt.wantSQL)
			}
			if !reflect.DeepEqual(q.Args, tt.wantArgs) {
				t.Errorf("args\n got: %#v\nwant: %#v", q.Args, tt.wantArgs)
			}
		})
	}
}

func TestCompileRefusesNonAudienceRules(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		channel Channel
	}{
		{"validation error", okRule([]Condition{city(OpIn, "Paris")}), ChannelSMS},
		// A rejected or unsupported rule must never become "send to all".
		{"rejected", Rule{Status: StatusRejected, AudienceScope: ScopeAll}, ChannelSMS},
		{"unsupported", Rule{Status: StatusUnsupported, AudienceScope: ScopeAll,
			Unsupported: []Unsupported{{Text: "x", Reason: ReasonUnknownField}}}, ChannelSMS},
		{"unknown channel", caseRule(), "push"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CompileAudience(tt.rule, tt.channel, testNow); !errors.Is(err, ErrNotCompilable) {
				t.Errorf("CompileAudience: want ErrNotCompilable, got %v", err)
			}
			if tt.channel == "push" {
				return
			}
			if _, err := CompileCounts(tt.rule, testNow); !errors.Is(err, ErrNotCompilable) {
				t.Errorf("CompileCounts: want ErrNotCompilable, got %v", err)
			}
		})
	}
}

func TestCompileCounts(t *testing.T) {
	r := okRule([]Condition{age(OpGte, 18)},
		group(city(OpIn, "İstanbul")),
		group(event(EventPurchase, false, 90)))

	q, err := CompileCounts(r, testNow)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := "SELECT count(*), " +
		"count(*) FILTER (WHERE u.sms_opt_in), " +
		"count(*) FILTER (WHERE u.email_opt_in), " +
		"count(*) FILTER (WHERE (u.city = ANY($1))), " +
		"count(*) FILTER (WHERE (NOT " + existsEvent + "$2 AND e.occurred_at >= $3))) " +
		"FROM users u WHERE (u.sms_opt_in OR u.email_opt_in) AND (" +
		"(u.age >= $4) AND (((u.city = ANY($1))) OR ((NOT " + existsEvent + "$2 AND e.occurred_at >= $3)))))"
	if q.SQL != want {
		t.Errorf("SQL\n got: %s\nwant: %s", q.SQL, want)
	}
	if len(q.Args) != 4 {
		t.Errorf("got %d args, want 4 (group placeholders are reused)", len(q.Args))
	}

	// Scan maps columns to fields in order; a fake scan function stands in
	// for pgx.Row.Scan.
	got, err := q.Scan(func(dest ...any) error {
		for i, d := range dest {
			*d.(*int64) = int64(100 + i)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	wantCounts := Counts{Reachable: 100, SMS: 101, Email: 102, Groups: []int64{103, 104}}
	if !reflect.DeepEqual(got, wantCounts) {
		t.Errorf("counts = %+v, want %+v", got, wantCounts)
	}
}
