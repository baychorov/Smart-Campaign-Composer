package segment

import (
	"fmt"
	"strings"
)

// Summary is a Turkish, human-readable view of a rule, generated from the
// rule itself rather than from LLM prose, so the marketer sees exactly what
// will run (D-SEG). The UI renders it as rule cards.
type Summary struct {
	All    bool       `json:"all"`    // audience_scope all: no conditions
	Common []string   `json:"common"` // one line per common condition
	Groups [][]string `json:"groups"` // one list per group; OR between groups
}

// String renders the summary as plain text.
func (s Summary) String() string {
	if s.All {
		return "Tüm kullanıcılar"
	}
	var b strings.Builder
	if len(s.Common) > 0 {
		b.WriteString("Tüm koşullar: " + strings.Join(s.Common, "; "))
	}
	if len(s.Groups) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Ve şu gruplardan en az biri:")
		for i, g := range s.Groups {
			fmt.Fprintf(&b, "\n  %d. grup: %s", i+1, strings.Join(g, "; "))
		}
	}
	return b.String()
}

// Summarize describes a normalised rule (use Result.Rule from Validate).
func Summarize(r Rule) Summary {
	if r.AudienceScope == ScopeAll {
		return Summary{All: true}
	}
	s := Summary{Common: describeAll(r.Common)}
	for _, g := range r.Groups {
		s.Groups = append(s.Groups, describeAll(g.Conditions))
	}
	return s
}

func describeAll(cs []Condition) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = Describe(c)
	}
	return out
}

var eventPhrases = map[EventType][2]string{ // [did, did not]
	EventSignup:      {"kayıt olmuş", "kayıt olmamış"},
	EventProductView: {"ürün incelemiş", "ürün incelememiş"},
	EventAddToCart:   {"sepete ürün eklemiş", "sepete ürün eklememiş"},
	EventPurchase:    {"alışveriş yapmış", "alışveriş yapmamış"},
}

// Describe returns a short Turkish phrase for one condition.
func Describe(c Condition) string {
	switch {
	case c.Field == FieldCity && c.Op == OpIn:
		return "şehri " + joinOr(c.Values) + " olan"
	case c.Field == FieldCity && c.Op == OpNotIn:
		return "şehri " + strings.Join(c.Values, ", ") + " dışında olan"
	case c.Field == FieldAge && c.Op == OpGte && c.Value != nil:
		return fmt.Sprintf("%d yaş ve üzeri", *c.Value)
	case c.Field == FieldAge && c.Op == OpLte && c.Value != nil:
		return fmt.Sprintf("%d yaş ve altı", *c.Value)
	case c.Type == KindEvent && c.Occurred != nil && c.WithinDays != nil:
		phrases, ok := eventPhrases[c.Event]
		if !ok {
			break
		}
		phrase := phrases[0]
		if !*c.Occurred {
			phrase = phrases[1]
		}
		return fmt.Sprintf("son %d günde %s", *c.WithinDays, phrase)
	}
	return "geçersiz koşul"
}

// joinOr renders ["A","B","C"] as "A, B veya C".
func joinOr(vs []string) string {
	if len(vs) <= 1 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " veya " + vs[len(vs)-1]
}
