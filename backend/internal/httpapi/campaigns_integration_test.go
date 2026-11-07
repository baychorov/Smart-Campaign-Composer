//go:build integration

package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/campaign"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

// The campaign routes end to end: HTTP → campaign.Service → Postgres, with
// a Fake LLM. Business rules are tested in package campaign; this checks
// the wiring, status codes and JSON shapes.

var seedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

type apiClient struct {
	t   *testing.T
	url string
}

func (c apiClient) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequestWithContext(c.t.Context(), method, c.url+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("%s %s: response is not JSON: %q", method, path, raw)
	}
	return resp.StatusCode, out
}

// newCampaignServer starts the API on a seeded database. fake answers the
// model calls in order.
func newCampaignServer(t *testing.T, fake *llm.Fake) (apiClient, *bytes.Buffer) {
	t.Helper()
	pool := pgtest.NewDB(t)
	if err := seed.Insert(t.Context(), pool, seed.Generate(seed.DefaultSeed, 2000, seedNow)); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	now := func() time.Time { return seedNow }
	api := New(Config{
		Campaigns:         campaign.NewService(pool, fake, now),
		LLM:               fake,
		LLMProvider:       "mock",
		Log:               slog.New(slog.NewJSONHandler(logs, nil)),
		GenerationTimeout: 10 * time.Second,
		Now:               now,
	})
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return apiClient{t: t, url: srv.URL}, logs
}

func campaignOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	c, ok := out["campaign"].(map[string]any)
	if !ok {
		t.Fatalf("no campaign in %v", out)
	}
	return c
}

func idPath(c map[string]any) string {
	return "/api/campaigns/" + fmtAny(c["id"])
}

func TestCampaignFlow(t *testing.T) {
	const brief = "İstanbul'da son 30 günde sepete ürün ekleyip alışveriş yapmayanlara hatırlatma."
	fake := llm.NewFake(
		llm.Text(caseRuleJSON),                // segment extraction
		llm.Text(messageText(t, goodMessage)), // content generation
	)
	api, logs := newCampaignServer(t, fake)

	status, out := api.do("POST", "/api/campaigns", `{"brief":"`+brief+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	c := campaignOf(t, out)
	path := idPath(c)
	aud := c["audience"].(map[string]any)
	if c["status"] != "draft" || c["approvable"] != true || aud["snapshot"] != false || aud["sms"].(float64) == 0 {
		t.Fatalf("draft %v", c)
	}
	if c["summary"].(map[string]any)["common"] == nil || out["warnings"] == nil {
		t.Fatalf("summary or warnings missing: %v", out)
	}
	if strings.Contains(logs.String(), "sepete ürün") {
		t.Error("the brief must not be logged")
	}

	// Generation reads brief and rule from the draft.
	status, out = api.do("POST", path+"/content/generations", "")
	if status != http.StatusAccepted || !strings.HasPrefix(out["events_url"].(string), "/api/content/generations/") {
		t.Fatalf("start generation: %d %v", status, out)
	}
	evs := streamEvents(t, api.url, out["id"].(string), "")
	if last := evs[len(evs)-1]; last.Name != "validated" {
		t.Fatalf("generation ended with %v", names(evs))
	}
	if prompt := fake.Calls()[1].Prompt; !strings.Contains(prompt, brief) || !strings.Contains(prompt, "şehri İstanbul olan") {
		t.Errorf("generation prompt lacks the stored brief or rule summary")
	}

	status, out = api.do("PUT", path+"/content", messageText(t, goodMessage))
	if status != http.StatusOK || out["validation"].(map[string]any)["valid"] != true {
		t.Fatalf("save content: %d %v", status, out)
	}

	status, out = api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":false}}`)
	if status != http.StatusOK {
		t.Fatalf("approve: %d %v", status, out)
	}
	c = campaignOf(t, out)
	snap := out["snapshot"].(map[string]any)
	if c["status"] != "approved" || snap["sms"] != aud["sms"] || snap["email"] != float64(0) {
		t.Fatalf("approved %v, snapshot %v, draft audience %v", c, snap, aud)
	}

	// Double click: the second approval is a conflict.
	status, out = api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true}}`)
	if status != http.StatusConflict || out["error"] != "not_draft" {
		t.Fatalf("second approve: %d %v", status, out)
	}
	status, out = api.do("GET", path, "")
	if status != http.StatusOK || campaignOf(t, out)["audience"].(map[string]any)["snapshot"] != true {
		t.Fatalf("get approved: %d %v", status, out)
	}
	if status, _ = api.do("PUT", path+"/content", messageText(t, goodMessage)); status != http.StatusConflict {
		t.Fatalf("edit after approval: %d", status)
	}
}

func TestCampaignContentValidation(t *testing.T) {
	// İstanbul audience: includes the 31-rune name cohort.
	istanbul := `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İstanbul"]}],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
	api, _ := newCampaignServer(t, llm.NewFake(llm.Text(istanbul)))
	_, out := api.do("POST", "/api/campaigns", `{"brief":"İstanbul"}`)
	path := idPath(campaignOf(t, out))

	msg := func(sms string) string {
		m := goodMessage
		m.SMS = sms
		return messageText(t, m)
	}
	tests := []struct {
		name  string
		body  string
		valid bool
		code  string
	}{
		{"160 Turkish characters", msg(strings.Repeat("ğ", 160)), true, ""},
		{"161 Turkish characters", msg(strings.Repeat("ğ", 161)), false, content.CodeTooLong},
		{"fits as written, not with the longest name", msg("{{first_name}}" + strings.Repeat("a", 140)), false, content.CodeTooLong},
		{"unknown variable", msg("Merhaba {{ad}}"), false, content.CodeUnknownVariable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out := api.do("PUT", path+"/content", tt.body)
			v := out["validation"].(map[string]any)
			if status != http.StatusOK || v["valid"] != tt.valid {
				t.Fatalf("status %d, body %v", status, out)
			}
			errs := v["errors"].([]any)
			if tt.code != "" && (len(errs) == 0 || errs[0].(map[string]any)["code"] != tt.code) {
				t.Fatalf("errors %v, want %s", errs, tt.code)
			}
			if strings.Contains(fmtAny(out), seed.LongestFirstName) {
				t.Error("response exposes a customer name")
			}
		})
	}
}

func TestCampaignErrors(t *testing.T) {
	rejected := `{"status":"rejected","audience_scope":"filtered","common":[],"groups":[],"assumptions":[],"unsupported":[{"text":"x","reason":"off_topic","group":null}],"notes":[]}`
	partial := `{"status":"partial","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İzmir"]}],"groups":[],"assumptions":[],"unsupported":[{"text":"en az 3 kez","reason":"aggregate","group":null}],"notes":[]}`
	invalid := `{"status":"ok","audience_scope":"filtered"}`
	fake := llm.NewFake(
		llm.Text(rejected),
		llm.Text(partial),
		llm.Text(invalid), llm.Text(invalid), llm.Text(invalid), // extraction gives up after repairs
		llm.FakeResponse{StopReason: llm.StopRefusal, Refusal: &llm.Refusal{Explanation: "Politika."}},
	)
	api, _ := newCampaignServer(t, fake)

	// Rejected rule: stored as a draft, but no audience and no generation.
	status, out := api.do("POST", "/api/campaigns", `{"brief":"Python sıralama algoritması yaz"}`)
	c := campaignOf(t, out)
	if status != http.StatusCreated || c["approvable"] != false || c["audience"] != nil {
		t.Fatalf("rejected draft: %d %v", status, out)
	}
	rejectedPath := idPath(c)
	if status, out = api.do("POST", rejectedPath+"/content/generations", ""); status != 422 || out["error"] != "rule_not_approvable" {
		t.Fatalf("generation for rejected rule: %d %v", status, out)
	}

	// Partial rule: approval needs the acknowledgement.
	_, out = api.do("POST", "/api/campaigns", `{"brief":"İzmirli sadık müşteriler"}`)
	partialPath := idPath(campaignOf(t, out))
	api.do("PUT", partialPath+"/content", messageText(t, goodMessage))
	status, out = api.do("POST", partialPath+"/approve", `{"channels":{"sms":true,"email":true}}`)
	issues, _ := out["issues"].([]any)
	if status != 422 || out["error"] != "approval_blocked" || len(issues) != 1 || issues[0].(map[string]any)["code"] != campaign.CodePartialNotAcked {
		t.Fatalf("partial without ack: %d %v", status, out)
	}

	// Rule edits are validated; an invalid rule is refused and not stored.
	status, out = api.do("PUT", partialPath+"/rule", `{"rule":`+invalid+`}`)
	if status != 422 || out["error"] != "invalid_rule" || len(out["issues"].([]any)) == 0 {
		t.Fatalf("invalid rule edit: %d %v", status, out)
	}
	status, out = api.do("PUT", partialPath+"/rule", `{"rule":`+caseRuleJSON+`}`)
	if status != 200 || campaignOf(t, out)["rule"].(map[string]any)["status"] != "ok" {
		t.Fatalf("valid rule edit: %d %v", status, out)
	}

	tests := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"model output invalid after repairs", "POST", "/api/campaigns", `{"brief":"b"}`, 422, "invalid_rule"},
		{"refusal", "POST", "/api/campaigns", `{"brief":"b"}`, 422, "refusal"},
		{"empty brief", "POST", "/api/campaigns", `{"brief":"  "}`, 400, "invalid_brief"},
		{"too long brief", "POST", "/api/campaigns", `{"brief":"` + strings.Repeat("ş", llm.MaxBriefRunes+1) + `"}`, 400, "invalid_brief"},
		{"not JSON", "POST", "/api/campaigns", `{brief`, 400, "invalid_json"},
		{"unknown field", "POST", partialPath + "/approve", `{"channels":{"sms":true},"sql":"x"}`, 400, "invalid_json"},
		{"unknown id", "GET", "/api/campaigns/999999", "", 404, "not_found"},
		{"non-numeric id", "GET", "/api/campaigns/abc", "", 404, "not_found"},
		{"approve unknown id", "POST", "/api/campaigns/999999/approve", `{"channels":{"sms":true}}`, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out := api.do(tt.method, tt.path, tt.body)
			if status != tt.status || out["error"] != tt.code {
				t.Fatalf("status %d, body %v", status, out)
			}
		})
	}
	// Six scripted replies, six model calls: no request above reached the
	// model by mistake, and every failed extraction stored nothing.
	if n := len(fake.Calls()); n != 6 {
		t.Errorf("%d model calls, want 6", n)
	}
}
