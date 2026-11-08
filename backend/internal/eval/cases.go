// Package eval measures segment extraction against the 20-brief test set in
// docs/PLAN.md (D-TEST). Each brief is sent to the model several times; a
// run is correct when its status, its unsupported reasons and the set of
// users its rule selects on a fixed seed match the expectation. Accuracy
// and consistency are reported separately.
package eval

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

//go:embed cases.json
var casesJSON []byte

// Outcome statuses beyond segment.Status: the model's rule never became
// valid (StatusInvalid), the model refused (StatusRefusal), or the call
// failed (StatusError).
const (
	StatusInvalid = "invalid"
	StatusRefusal = "refusal"
	StatusError   = "error"
)

// How a case's audience is scored.
const (
	// AudienceExact: the rule must select exactly the users of one of the
	// expected rules.
	AudienceExact = "exact"
	// AudienceAny: the rule must describe some audience, but which one
	// depends on an assumption (e.g. "for a long time"); it is not scored.
	AudienceAny = "any"
	// AudienceNone: no approvable rule may come out.
	AudienceNone = "none"
)

// Case is one brief of the test set with its expectation.
type Case struct {
	ID       int    `json:"id"`
	Category string `json:"category"`
	Brief    string `json:"brief"`
	// Status lists the accepted outcome statuses; the first is preferred.
	Status   []string `json:"status"`
	Audience string   `json:"audience"`
	// Rules are the accepted audiences (only common, groups and
	// audience_scope matter). The first one is the reference that
	// differences are reported against.
	Rules []segment.Rule `json:"rules"`
	// Reasons must all appear in the rule's unsupported list.
	Reasons         []segment.Reason `json:"reasons"`
	NeedsAssumption bool             `json:"needs_assumption"`
}

// LoadCases returns the embedded test set after checking it.
func LoadCases() ([]Case, error) {
	var cases []Case
	if err := json.Unmarshal(casesJSON, &cases); err != nil {
		return nil, fmt.Errorf("parse cases.json: %w", err)
	}
	if err := checkCases(cases); err != nil {
		return nil, err
	}
	return cases, nil
}

// checkCases catches mistakes in cases.json itself: an expected rule that
// does not validate would make every run look wrong.
func checkCases(cases []Case) error {
	seen := map[int]bool{}
	for i := range cases {
		c := &cases[i]
		switch {
		case c.ID <= 0 || seen[c.ID]:
			return fmt.Errorf("case %d: missing or duplicate id", c.ID)
		case c.Brief == "" || c.Category == "" || len(c.Status) == 0:
			return fmt.Errorf("case %d: brief, category and status are required", c.ID)
		case !slices.Contains([]string{AudienceExact, AudienceAny, AudienceNone}, c.Audience):
			return fmt.Errorf("case %d: unknown audience %q", c.ID, c.Audience)
		case c.Audience == AudienceExact && len(c.Rules) == 0:
			return fmt.Errorf("case %d: exact audience needs at least one rule", c.ID)
		}
		seen[c.ID] = true
		for j, r := range c.Rules {
			res := segment.Validate(expectedRule(r))
			if !res.Approvable() || len(res.Warnings) > 0 {
				return fmt.Errorf("case %d: rule %d is not a clean approvable rule: errors %v, warnings %v",
					c.ID, j, res.Errors, res.Warnings)
			}
			c.Rules[j] = res.Rule
		}
	}
	return nil
}

// expectedRule turns the audience part written in cases.json into a full
// ok rule.
func expectedRule(r segment.Rule) segment.Rule {
	scope := r.AudienceScope
	if scope == "" {
		scope = segment.ScopeFiltered
	}
	return segment.Rule{Status: segment.StatusOK, AudienceScope: scope, Common: r.Common, Groups: r.Groups}
}

// IdealReply is the answer a perfect model would give for c. make eval
// -dry-run and the tests send it through the real pipeline instead of
// calling the API.
func IdealReply(c Case) segment.Rule {
	r := segment.Rule{Status: segment.Status(c.Status[0]), AudienceScope: segment.ScopeFiltered}
	if len(c.Rules) > 0 {
		r.AudienceScope = c.Rules[0].AudienceScope
		r.Common = c.Rules[0].Common
		r.Groups = c.Rules[0].Groups
	}
	reasons := c.Reasons
	if len(reasons) == 0 && r.Status == segment.StatusRejected {
		reasons = []segment.Reason{segment.ReasonOffTopic}
	}
	for _, reason := range reasons {
		r.Unsupported = append(r.Unsupported, segment.Unsupported{Text: "eval: " + string(reason), Reason: reason})
	}
	if c.NeedsAssumption {
		r.Assumptions = []string{"eval: varsayım"}
	}
	return r
}
