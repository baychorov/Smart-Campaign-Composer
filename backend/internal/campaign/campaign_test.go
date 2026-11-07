package campaign

import (
	"slices"
	"strings"
	"testing"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

func TestCanTransition(t *testing.T) {
	all := []Status{StatusDraft, StatusApproved, StatusSending, StatusCompleted}
	allowed := map[[2]Status]bool{
		{StatusDraft, StatusApproved}:    true,
		{StatusApproved, StatusSending}:  true,
		{StatusSending, StatusCompleted}: true,
	}
	// Every pair, so a new edge cannot be added by accident: no way back
	// to draft, no skipping approval, nothing after completed.
	for _, from := range all {
		for _, to := range all {
			if got := CanTransition(from, to); got != allowed[[2]Status{from, to}] {
				t.Errorf("CanTransition(%s, %s) = %v", from, to, got)
			}
		}
	}
}

func validated(t *testing.T, r segment.Rule) segment.Result {
	t.Helper()
	v := segment.Validate(r)
	if !v.Valid() {
		t.Fatalf("test rule invalid: %v", v.Errors)
	}
	return v
}

func codes(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

func TestPrecheck(t *testing.T) {
	istanbul := segment.Condition{Type: segment.KindAttribute, Field: segment.FieldCity, Op: segment.OpIn, Values: []string{"İstanbul"}}
	ok := segment.Rule{Status: segment.StatusOK, AudienceScope: segment.ScopeFiltered, Common: []segment.Condition{istanbul}}
	partial := ok
	partial.Status = segment.StatusPartial
	partial.Unsupported = []segment.Unsupported{{Text: "en az 3 kez", Reason: segment.ReasonAggregate}}
	rejected := segment.Rule{Status: segment.StatusRejected, AudienceScope: segment.ScopeFiltered,
		Unsupported: []segment.Unsupported{{Text: "x", Reason: segment.ReasonOffTopic}}}
	both := ApproveRequest{SMS: true, Email: true}

	tests := []struct {
		name string
		rule segment.Rule
		req  ApproveRequest
		want []string
	}{
		{"ok rule, both channels", ok, both, nil},
		{"ok rule, sms only", ok, ApproveRequest{SMS: true}, nil},
		{"no channel", ok, ApproveRequest{}, []string{CodeNoChannel}},
		{"partial without ack", partial, both, []string{CodePartialNotAcked}},
		{"partial with ack", partial, ApproveRequest{SMS: true, Email: true, PartialAck: true}, nil},
		{"ack on an ok rule is harmless", ok, ApproveRequest{Email: true, PartialAck: true}, nil},
		{"rejected rule", rejected, both, []string{CodeRuleNotApproved}},
		{"rejected rule, ack does not help", rejected, ApproveRequest{SMS: true, PartialAck: true}, []string{CodeRuleNotApproved}},
		{"every problem at once", partial, ApproveRequest{}, []string{CodeNoChannel, CodePartialNotAcked}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codes(precheck(validated(t, tt.rule), tt.req))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("codes %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContentIssuesFollowChannels(t *testing.T) {
	// Empty SMS and too-long subject: each matters only on its channel.
	res := content.Validate(content.Message{
		SMS:          "",
		EmailSubject: "Konu " + strings.Repeat("ş", content.MaxEmailSubjectRunes),
		EmailBody:    "Gövde",
	}, nil)

	tests := []struct {
		name string
		req  ApproveRequest
		want []string
	}{
		{"sms only", ApproveRequest{SMS: true}, []string{"message.sms"}},
		{"email only", ApproveRequest{Email: true}, []string{"message.email_subject"}},
		{"both", ApproveRequest{SMS: true, Email: true}, []string{"message.sms", "message.email_subject"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			for _, i := range contentIssues(res, tt.req) {
				paths = append(paths, i.Path)
			}
			if !slices.Equal(paths, tt.want) {
				t.Fatalf("paths %v, want %v", paths, tt.want)
			}
		})
	}
}
