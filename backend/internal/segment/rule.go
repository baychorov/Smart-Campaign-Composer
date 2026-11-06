// Package segment defines the audience rule produced by the LLM, validates
// and normalises it, and compiles it to parameterised SQL. It is pure: no
// I/O, no DB access. Callers run the compiled query.
//
// Audience = consent ∧ common ∧ (group₁ ∨ … ∨ groupₙ)   (DECISIONS D-SEG)
package segment

// Status is the LLM's verdict on how much of the brief it could express.
type Status string

const (
	StatusOK          Status = "ok"
	StatusPartial     Status = "partial"
	StatusUnsupported Status = "unsupported"
	StatusRejected    Status = "rejected"
)

// AudienceScope distinguishes "everyone" from "filtered by conditions".
type AudienceScope string

const (
	ScopeFiltered AudienceScope = "filtered"
	ScopeAll      AudienceScope = "all"
)

// Kind selects which fields of a Condition are meaningful.
type Kind string

const (
	KindAttribute Kind = "attribute"
	KindEvent     Kind = "event"
)

// Field is a user attribute that a condition can filter on.
type Field string

const (
	FieldCity Field = "city"
	FieldAge  Field = "age"
)

// Op is an attribute comparison.
type Op string

const (
	OpIn    Op = "in"     // city
	OpNotIn Op = "not_in" // city
	OpGte   Op = "gte"    // age
	OpLte   Op = "lte"    // age
)

// EventType is a tracked user event.
type EventType string

const (
	EventSignup      EventType = "signup"
	EventProductView EventType = "product_view"
	EventAddToCart   EventType = "add_to_cart"
	EventPurchase    EventType = "purchase"
)

// Reason is the closed set of codes for criteria the rule cannot express.
type Reason string

const (
	ReasonSequence      Reason = "sequence"
	ReasonAggregate     Reason = "aggregate"
	ReasonRanking       Reason = "ranking"
	ReasonUnknownField  Reason = "unknown_field"
	ReasonAbsoluteDate  Reason = "absolute_date"
	ReasonScheduling    Reason = "scheduling"
	ReasonContradiction Reason = "contradiction"
	ReasonTooComplex    Reason = "too_complex"
	ReasonOffTopic      Reason = "off_topic"
)

// Channel is a delivery channel; each has its own consent column.
type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

// Limits from D-SEG. Exceeding any of them is a too_complex error.
const (
	MaxGroups          = 5
	MaxGroupConditions = 6
	MaxCommonConds     = 6
	MaxTotalConditions = 20
	MaxInValues        = 20
	MinWithinDays      = 1
	MaxWithinDays      = 365
	MinAge             = 13
	MaxAge             = 100
)

// Rule is the structured output of segment extraction.
type Rule struct {
	Status        Status        `json:"status"`
	AudienceScope AudienceScope `json:"audience_scope"`
	Common        []Condition   `json:"common"`
	Groups        []Group       `json:"groups"`
	Assumptions   []string      `json:"assumptions"`
	Unsupported   []Unsupported `json:"unsupported"`
	Notes         []string      `json:"notes"`
}

// Group is a conjunction of conditions; groups are OR-ed together.
type Group struct {
	Conditions []Condition `json:"conditions"`
}

// Unsupported records a criterion from the brief the rule does not apply.
type Unsupported struct {
	Text   string `json:"text"`
	Reason Reason `json:"reason"`
	// Group is the 0-based index of the group the criterion belonged to,
	// or nil when it applied to the whole brief.
	Group *int `json:"group"`
}

// Condition is either an attribute filter or an event filter, selected by
// Type. One flat struct (instead of an interface) keeps JSON decoding with
// DisallowUnknownFields simple; validation enforces which fields are set.
type Condition struct {
	Type Kind `json:"type"`

	// Attribute: city uses Values, age uses Value.
	Field  Field    `json:"field,omitempty"`
	Op     Op       `json:"op,omitempty"`
	Values []string `json:"values,omitempty"`
	Value  *int     `json:"value,omitempty"`

	// Event: Occurred=true means "did X in the last WithinDays days",
	// false means "did not".
	Event      EventType `json:"event,omitempty"`
	Occurred   *bool     `json:"occurred,omitempty"`
	WithinDays *int      `json:"within_days,omitempty"`
}
