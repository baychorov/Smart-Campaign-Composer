//go:build live

package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
)

// TestContentVariablesLive: real API, paid, at most 1+MaxRepairs (3) calls.
// It is not part of `make test-live` (the name does not start with
// TestLive) and runs only when named, with the user's approval:
//
//	go test -count=1 -v -tags live -run '^TestContentVariablesLive$' ./internal/llm/
//
// It generates the copy of the UI's default brief once and checks the
// variable rules of PLAN step 15f: only {{first_name}}, no suffix on it, and
// no mention of the audience's city. One run is a spot check, not a
// measurement.
func TestContentVariablesLive(t *testing.T) {
	c, model := liveAdapter(t)
	in := ContentInput{
		Brief:    "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.",
		Audience: "Tüm koşullar: şehri İstanbul olan; son 30 günde sepete ürün eklemiş; son 30 günde alışveriş yapmamış",
		Notes:    []string{"Samimi ama kısa bir dil istendi.", "%30 indirim hatırlatması."},
	}
	longest := content.Values{content.VarFirstName: "Muhammed Abdurrahman Şahabettin"}

	res, err := GenerateContent(context.Background(), c, in, longest, func(ContentEvent) {})
	t.Logf("model %s: %d call(s), %d repair(s)", model, len(res.Attempts), res.Repairs)
	for _, a := range res.Attempts {
		t.Logf("  attempt: stop %s, %d in / %d out tokens, issues %v", a.StopReason, a.Usage.InputTokens, a.Usage.OutputTokens, a.IssueCodes)
	}
	if err != nil {
		t.Fatalf("generate: %v (last issues %v)", err, res.Issues)
	}
	m := res.Validation.Message
	t.Logf("sms: %s\nsubject: %s\nbody:\n%s", m.SMS, m.EmailSubject, m.EmailBody)
	if !res.Validation.Valid() {
		t.Fatalf("not valid: %v", res.Validation.Errors)
	}
	if res.Repairs > 0 {
		t.Log("NOTE: a repair was needed; the attempts above show why")
	}
	for _, f := range []string{m.SMS, m.EmailSubject, m.EmailBody} {
		if strings.Contains(f, "{{city}}") || strings.Contains(f, "İstanbul") {
			t.Errorf("the city is in the copy: %q", f)
		}
	}
}
