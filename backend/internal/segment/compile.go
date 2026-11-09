package segment

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrNotCompilable is returned when a rule has validation errors or does
// not describe an audience (status unsupported or rejected).
var ErrNotCompilable = errors.New("rule is not compilable")

// Query is compiled SQL plus its parameters. SQL text is built only from
// constants in this file; every value from the rule travels in Args (R-AUD-1).
type Query struct {
	SQL  string
	Args []any
}

// Fixed identifier tables. Rule fields are looked up here; a rule can never
// put its own text into the SQL.
var (
	attributeColumns = map[Field]string{
		FieldCity: "u.city",
		FieldAge:  "u.age",
	}
	consentColumns = map[Channel]string{
		ChannelSMS:   "u.sms_opt_in",
		ChannelEmail: "u.email_opt_in",
	}
)

// anyConsent matches users reachable on at least one channel.
const anyConsent = "(u.sms_opt_in OR u.email_opt_in)"

// CompileAudience returns a query selecting the ids of users who match the
// rule and have consent for channel:
//
//	SELECT u.id FROM users u WHERE <consent> AND (<rule>)
//
// The consent filter is added here, outside the rule's own parentheses, so
// no rule content can remove or bypass it (R-AUD-2).
func CompileAudience(r Rule, channel Channel, now time.Time) (Query, error) {
	consent, ok := consentColumns[channel]
	if !ok {
		return Query{}, fmt.Errorf("%w: unknown channel %q", ErrNotCompilable, channel)
	}
	rule, err := compilable(r)
	if err != nil {
		return Query{}, err
	}

	b := &builder{now: now}
	where := b.rule(rule)
	return Query{
		SQL:  "SELECT u.id FROM users u WHERE " + consent + " AND (" + where + ")",
		Args: b.args,
	}, nil
}

// Counts is the result shape of CompileCounts.
type Counts struct {
	Reachable int64   // matches the rule and has consent for at least one channel
	SMS       int64   // matches and has SMS consent
	Email     int64   // matches and has email consent
	Groups    []int64 // reachable users per group, in group order; nil without groups
}

// CountQuery is a compiled count query. Its column count depends on the
// number of groups, so use Scan to read the result row.
type CountQuery struct {
	Query
	groups int
}

// Scan reads one result row via scan, which is typically pgx.Row.Scan.
func (q CountQuery) Scan(scan func(dest ...any) error) (Counts, error) {
	var c Counts
	dest := []any{&c.Reachable, &c.SMS, &c.Email}
	if q.groups > 0 {
		c.Groups = make([]int64, q.groups)
		for i := range c.Groups {
			dest = append(dest, &c.Groups[i])
		}
	}
	if err := scan(dest...); err != nil {
		return Counts{}, fmt.Errorf("scan counts: %w", err)
	}
	return c, nil
}

// CompileCounts returns one query that yields reachable, sms and email
// counts plus one reachable count per group. Every count includes a
// consent filter. Read the row with CountQuery.Scan.
func CompileCounts(r Rule, now time.Time) (CountQuery, error) {
	rule, err := compilable(r)
	if err != nil {
		return CountQuery{}, err
	}

	b := &builder{now: now}
	cols := []string{
		"count(*)",
		"count(*) FILTER (WHERE " + consentColumns[ChannelSMS] + ")",
		"count(*) FILTER (WHERE " + consentColumns[ChannelEmail] + ")",
	}
	// Group fragments are compiled once; their placeholders are reused in
	// the WHERE clause, which Postgres allows.
	groupSQL := make([]string, len(rule.Groups))
	for i, g := range rule.Groups {
		groupSQL[i] = b.conjunction(g.Conditions)
		cols = append(cols, "count(*) FILTER (WHERE "+groupSQL[i]+")")
	}
	where := b.combine(b.conjunction(rule.Common), groupSQL)

	return CountQuery{
		Query: Query{
			SQL:  "SELECT " + strings.Join(cols, ", ") + " FROM users u WHERE " + anyConsent + " AND (" + where + ")",
			Args: b.args,
		},
		groups: len(rule.Groups),
	}, nil
}

// compilable validates r and returns the normalised rule if it describes an
// audience.
func compilable(r Rule) (Rule, error) {
	res := Validate(r)
	if !res.Valid() {
		return Rule{}, fmt.Errorf("%w: %d validation error(s), first: %s %s", ErrNotCompilable,
			len(res.Errors), res.Errors[0].Path, res.Errors[0].Code)
	}
	if !res.Approvable() {
		return Rule{}, fmt.Errorf("%w: status %s", ErrNotCompilable, res.Rule.Status)
	}
	return res.Rule, nil
}

// builder accumulates positional parameters while emitting SQL fragments.
type builder struct {
	now  time.Time
	args []any
}

// arg stores v as the next parameter and returns its placeholder ($n).
func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

// rule compiles common ∧ (g1 ∨ g2 ∨ …). An empty rule (scope all) is TRUE.
// Common is compiled first so placeholders are numbered in reading order.
func (b *builder) rule(r Rule) string {
	common := b.conjunction(r.Common)
	groups := make([]string, len(r.Groups))
	for i, g := range r.Groups {
		groups[i] = b.conjunction(g.Conditions)
	}
	return b.combine(common, groups)
}

func (b *builder) combine(common string, groups []string) string {
	parts := []string{}
	if common != "" {
		parts = append(parts, common)
	}
	if len(groups) > 0 {
		parts = append(parts, "(("+strings.Join(groups, ") OR (")+"))")
	}
	if len(parts) == 0 {
		return "TRUE"
	}
	return strings.Join(parts, " AND ")
}

// conjunction ANDs conditions; each is wrapped in parentheses.
func (b *builder) conjunction(cs []Condition) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = "(" + b.condition(c) + ")"
	}
	return strings.Join(parts, " AND ")
}

func (b *builder) condition(c Condition) string {
	switch c.Type {
	case KindAttribute:
		col := attributeColumns[c.Field]
		switch c.Op {
		case OpIn:
			return col + " = ANY(" + b.arg(c.Values) + ")"
		case OpNotIn:
			return col + " <> ALL(" + b.arg(c.Values) + ")"
		case OpGte:
			return col + " >= " + b.arg(*c.Value)
		case OpLte:
			return col + " <= " + b.arg(*c.Value)
		}
	case KindEvent:
		return b.event(c)
	}
	// Unreachable for a validated rule; fail closed rather than emit SQL.
	panic(fmt.Sprintf("segment: uncompilable condition %+v", c))
}

// event compiles an event condition. Every form is an EXISTS over
// user_events (or a count for MinCount) that the index on (user_id,
// event_type, occurred_at) serves:
//
//	did X in window:              EXISTS (X in window)
//	did not:                      NOT EXISTS (X in window)
//	at least n times:             (SELECT count(*) … X in window) >= n
//	X, then (not) Y:              EXISTS (X in window AND [NOT] EXISTS (Y after that X))
//
// "Then not Y" holds when some X in the window has no Y after it; that is
// the same as "no Y after the last X in the window".
func (b *builder) event(c Condition) string {
	match := "e.user_id = u.id AND e.event_type = " + b.arg(string(c.Event)) + " AND " + b.window(c)
	switch {
	case !*c.Occurred:
		return "NOT EXISTS (SELECT 1 FROM user_events e WHERE " + match + ")"
	case c.MinCount != nil:
		return "(SELECT count(*) FROM user_events e WHERE " + match + ") >= " + b.arg(*c.MinCount)
	case c.Then != nil:
		after := "EXISTS (SELECT 1 FROM user_events f WHERE f.user_id = u.id AND f.event_type = " +
			b.arg(string(c.Then.Event)) + " AND f.occurred_at > e.occurred_at)"
		if !*c.Then.Occurred {
			after = "NOT " + after
		}
		return "EXISTS (SELECT 1 FROM user_events e WHERE " + match + " AND " + after + ")"
	default:
		return "EXISTS (SELECT 1 FROM user_events e WHERE " + match + ")"
	}
}

// window compiles the time window of an event condition on e.occurred_at.
// Both forms are computed in Go and passed as parameters.
func (b *builder) window(c Condition) string {
	if c.WithinDays != nil {
		since := b.now.Add(-time.Duration(*c.WithinDays) * 24 * time.Hour)
		return "e.occurred_at >= " + b.arg(since)
	}
	from, to := DateRange(c)
	return "e.occurred_at >= " + b.arg(from) + " AND e.occurred_at < " + b.arg(to)
}
