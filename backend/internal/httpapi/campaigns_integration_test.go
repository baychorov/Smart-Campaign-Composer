//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/campaign"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform"
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

// campaignEnv is the API on a seeded database, wired like cmd/api:
// metrics registry, HTTP metrics middleware and /metrics.
type campaignEnv struct {
	api  apiClient
	logs *bytes.Buffer
	pool *pgxpool.Pool
	srv  *Server
	reg  *prometheus.Registry
}

// newCampaignEnv starts the API. fake answers the model calls in order.
func newCampaignEnv(t *testing.T, fake *llm.Fake) campaignEnv {
	t.Helper()
	pool := pgtest.NewDB(t)
	if err := seed.Insert(t.Context(), pool, seed.Generate(seed.DefaultSeed, 2000, seedNow)); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	now := func() time.Time { return seedNow }
	reg := platform.NewRegistry()
	srv := New(Config{
		Campaigns:         campaign.NewService(pool, fake, now),
		Metrics:           reg,
		LLM:               fake,
		LLMProvider:       "mock",
		Log:               slog.New(slog.NewJSONHandler(logs, nil)),
		GenerationTimeout: 10 * time.Second,
		Now:               now,
	})
	srv.progressEvery = 20 * time.Millisecond
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", platform.MetricsHandler(reg))
	srv.Register(mux)
	ts := httptest.NewServer(platform.NewHTTPMetrics(reg).Wrap(mux))
	t.Cleanup(ts.Close)
	return campaignEnv{api: apiClient{t: t, url: ts.URL}, logs: logs, pool: pool, srv: srv, reg: reg}
}

// newCampaignServer is newCampaignEnv for tests that need only the client.
func newCampaignServer(t *testing.T, fake *llm.Fake) (apiClient, *bytes.Buffer) {
	t.Helper()
	env := newCampaignEnv(t, fake)
	return env.api, env.logs
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

// TestCampaignStoredContentValidation: GET and rule edits return the
// validation of the stored message against the current audience, so the UI
// never shows a result computed for an older rule.
func TestCampaignStoredContentValidation(t *testing.T) {
	istanbul := `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İstanbul"]}],"groups":[],"assumptions":[],"unsupported":[],"notes":[]}`
	api, _ := newCampaignServer(t, llm.NewFake(llm.Text(istanbul)))
	_, out := api.do("POST", "/api/campaigns", `{"brief":"İstanbul"}`)
	c := campaignOf(t, out)
	path := idPath(c)
	if c["content_validation"] != nil {
		t.Fatalf("draft without message has a validation: %v", c["content_validation"])
	}

	// 130 runes + {{first_name}}: too long with the 31-rune name of the
	// İstanbul audience, fits the case audience (D-CONTENT example).
	m := goodMessage
	m.SMS = "{{first_name}}" + strings.Repeat("a", 130)
	api.do("PUT", path+"/content", messageText(t, m))

	validation := func(out map[string]any) map[string]any {
		t.Helper()
		v, ok := campaignOf(t, out)["content_validation"].(map[string]any)
		if !ok {
			t.Fatalf("no content_validation in %v", out)
		}
		return v
	}
	_, out = api.do("GET", path, "")
	if v := validation(out); v["valid"] != false || len(v["errors"].([]any)) != 1 {
		t.Fatalf("İstanbul audience: %v", v)
	}
	status, out := api.do("PUT", path+"/rule", `{"rule":`+caseRuleJSON+`}`)
	if v := validation(out); status != http.StatusOK || v["valid"] != true {
		t.Fatalf("case audience after rule edit: %d %v", status, v)
	}
	sms := validation(out)["stats"].(map[string]any)["sms"].(map[string]any)
	if sms["runes"].(float64) >= sms["worst_case_runes"].(float64) {
		t.Errorf("worst case does not count the variable: %v", sms)
	}
	if strings.Contains(fmtAny(out), seed.LongestFirstName) {
		t.Error("response exposes a customer name")
	}

	if status, _ := api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true}}`); status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}
	_, out = api.do("GET", path, "")
	if campaignOf(t, out)["content_validation"] != nil {
		t.Error("approved campaign still reports a draft validation")
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
	env := newCampaignEnv(t, fake)
	api := env.api

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

	// Segment metrics: two extractions stored a rule, one gave up after
	// repairs, one was refused. Invalid briefs never reached the model.
	m := env.srv.llm
	for _, tt := range []struct {
		name string
		c    prometheus.Collector
		want float64
	}{
		{"ok", m.requests.WithLabelValues(opSegment, "ok"), 2},
		{"invalid", m.requests.WithLabelValues(opSegment, "invalid"), 1},
		{"refusal", m.requests.WithLabelValues(opSegment, "refusal"), 1},
		{"repairs", m.repairs.WithLabelValues(opSegment), llm.MaxRepairs},
		{"rejected rules", m.ruleStatus.WithLabelValues("rejected"), 1},
		{"partial rules", m.ruleStatus.WithLabelValues("partial"), 1},
	} {
		if got := testutil.ToFloat64(tt.c); got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
	if n := testutil.CollectAndCount(m.requests); n != 3 {
		t.Errorf("%d request series, want 3", n)
	}
	if got := testutil.ToFloat64(m.validationErrors); got < float64(llm.MaxRepairs+1) {
		t.Errorf("validation errors %v, want at least one per invalid attempt", got)
	}
}

// TestCampaignProgressStream: the dashboard stream sends the summary
// repeatedly and closes after the campaign is completed (R-OBS-1).
func TestCampaignProgressStream(t *testing.T) {
	env := newCampaignEnv(t, llm.NewFake(llm.Text(caseRuleJSON)))
	_, out := env.api.do("POST", "/api/campaigns", `{"brief":"İstanbul sepet hatırlatma"}`)
	path := idPath(campaignOf(t, out))
	env.api.do("PUT", path+"/content", messageText(t, goodMessage))
	if status, out := env.api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true}}`); status != http.StatusOK {
		t.Fatalf("approve: %d %v", status, out)
	}

	// Finish the campaign a little later, as the worker would.
	id := campaignOf(t, out)["id"]
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = env.pool.Exec(t.Context(), `UPDATE deliveries SET status = 'sent', sent_at = $2 WHERE campaign_id = $1`, id, seedNow)
		_, _ = env.pool.Exec(t.Context(), `UPDATE campaigns SET status = 'completed' WHERE id = $1`, id)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.api.url+path+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	evs := readEvents(t, resp.Body) // returns when the stream closes
	if ctx.Err() != nil {
		t.Fatal("stream did not close after completion; the client timeout ended it")
	}
	if len(evs) < 3 {
		t.Fatalf("%d events, want several updates before completion", len(evs))
	}
	first, last := evs[0].Data, evs[len(evs)-1].Data
	if first["status"] != "approved" || first["done"] != false {
		t.Fatalf("first event %v", first)
	}
	sms := last["channels"].(map[string]any)["sms"].(map[string]any)
	if last["status"] != "completed" || last["done"] != true || sms["sent"] != sms["total"] || sms["pending"] != float64(0) {
		t.Fatalf("last event %v", last)
	}
	for i, e := range evs {
		if e.Name != "progress" || e.ID != fmtAny(i) {
			t.Fatalf("event %d: %s id %s", i, e.Name, e.ID)
		}
	}

	if status, out := env.api.do("GET", "/api/campaigns/999999/events", ""); status != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("unknown campaign: %d %v", status, out)
	}

	// /metrics: route patterns as labels, no brief text, names or ids.
	r, err := http.Get(env.api.url + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	metrics := string(body)
	for _, want := range []string{`route="GET /api/campaigns/{id}/events"`, `route="POST /api/campaigns/{id}/approve"`, "scc_llm_requests_total", "go_goroutines"} {
		if !strings.Contains(metrics, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
	for _, leak := range []string{"sepet", seed.LongestFirstName, path} {
		if strings.Contains(metrics, leak) {
			t.Errorf("/metrics contains %q", leak)
		}
	}
}

// deleteCampaign sends DELETE and returns the status; a 204 has no body.
func (c apiClient) deleteCampaign(path string) int {
	c.t.Helper()
	req, _ := http.NewRequestWithContext(c.t.Context(), "DELETE", c.url+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestCampaignListAndDelete(t *testing.T) {
	fake := llm.NewFake(llm.Text(caseRuleJSON), llm.Text(caseRuleJSON), llm.Text(caseRuleJSON))
	env := newCampaignEnv(t, fake)
	api := env.api

	create := func() map[string]any {
		t.Helper()
		status, out := api.do("POST", "/api/campaigns", `{"brief":"İstanbul'da sepeti terk edenler"}`)
		if status != http.StatusCreated {
			t.Fatalf("create: %d %v", status, out)
		}
		return campaignOf(t, out)
	}
	draft1, sent, draft2 := create(), create(), create()
	if status, out := api.do("PUT", idPath(sent)+"/content", messageText(t, goodMessage)); status != http.StatusOK {
		t.Fatalf("save content: %d %v", status, out)
	}
	if status, out := api.do("POST", idPath(sent)+"/approve", `{"channels":{"sms":true,"email":false}}`); status != http.StatusOK {
		t.Fatalf("approve: %d %v", status, out)
	}

	listIDs := func(out map[string]any) []any {
		var got []any
		for _, it := range out["campaigns"].([]any) {
			got = append(got, it.(map[string]any)["id"])
		}
		return got
	}

	// Newest first; a row has no brief, rule or message.
	status, out := api.do("GET", "/api/campaigns", "")
	if status != http.StatusOK || fmt.Sprint(listIDs(out)) != fmt.Sprint([]any{draft2["id"], sent["id"], draft1["id"]}) || out["next_before"] != nil {
		t.Fatalf("list: %d %v", status, out)
	}
	row := out["campaigns"].([]any)[1].(map[string]any)
	for _, k := range []string{"brief", "rule", "message"} {
		if _, ok := row[k]; ok {
			t.Errorf("list row has %q", k)
		}
	}
	progress := row["progress"].(map[string]any)
	if _, ok := progress["email"]; ok || progress["sms"].(map[string]any)["total"].(float64) == 0 {
		t.Fatalf("progress of an SMS-only campaign: %v", progress)
	}

	// Filter and paging.
	status, out = api.do("GET", "/api/campaigns?filter=sent", "")
	if status != http.StatusOK || fmt.Sprint(listIDs(out)) != fmt.Sprint([]any{sent["id"]}) {
		t.Fatalf("sent filter: %d %v", status, out)
	}
	status, out = api.do("GET", "/api/campaigns?limit=2", "")
	if status != http.StatusOK || out["next_before"] != sent["id"] {
		t.Fatalf("first page: %d %v", status, out)
	}
	status, out = api.do("GET", "/api/campaigns?limit=2&before="+fmtAny(sent["id"]), "")
	if status != http.StatusOK || fmt.Sprint(listIDs(out)) != fmt.Sprint([]any{draft1["id"]}) {
		t.Fatalf("second page: %d %v", status, out)
	}
	for _, q := range []string{"?filter=archived", "?before=0", "?before=x", "?limit=-1"} {
		if status, out = api.do("GET", "/api/campaigns"+q, ""); status != http.StatusBadRequest || out["error"] != "invalid_query" {
			t.Errorf("%s: %d %v", q, status, out)
		}
	}

	// Delete: drafts only.
	if status := api.deleteCampaign(idPath(draft1)); status != http.StatusNoContent {
		t.Fatalf("delete draft: %d", status)
	}
	if status, _ = api.do("GET", idPath(draft1), ""); status != http.StatusNotFound {
		t.Fatalf("get deleted draft: %d", status)
	}
	if status := api.deleteCampaign(idPath(draft1)); status != http.StatusNotFound {
		t.Fatalf("delete twice: %d", status)
	}
	if status := api.deleteCampaign(idPath(sent)); status != http.StatusConflict {
		t.Fatalf("delete approved: %d", status)
	}
	status, out = api.do("GET", "/api/campaigns", "")
	if fmt.Sprint(listIDs(out)) != fmt.Sprint([]any{draft2["id"], sent["id"]}) {
		t.Fatalf("list after delete: %d %v", status, out)
	}
	if !strings.Contains(env.logs.String(), "draft campaign deleted") {
		t.Error("deletion is not logged")
	}
}

func TestScheduleAndCancel(t *testing.T) {
	// The model suggests tomorrow 10:00 (Turkey) for the first campaign.
	suggested := strings.Replace(caseRuleJSON, `"notes":[`, `"send_at":"2026-06-16T10:00:00+03:00","notes":[`, 1)
	if suggested == caseRuleJSON {
		t.Fatal("test rule has no notes field to extend")
	}
	fake := llm.NewFake(llm.Text(suggested), llm.Text(caseRuleJSON))
	env := newCampaignEnv(t, fake)
	api := env.api

	status, out := api.do("POST", "/api/campaigns", `{"brief":"İstanbul'da sepeti terk edenlere yarın 10'da gönder"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	c := campaignOf(t, out)
	path := idPath(c)
	if !sameInstant(c["send_at"], "2026-06-16T07:00:00Z") || c["status"] != "draft" {
		t.Fatalf("draft send_at %v, status %v", c["send_at"], c["status"])
	}
	if status, out = api.do("PUT", path+"/content", messageText(t, goodMessage)); status != http.StatusOK {
		t.Fatalf("save content: %d %v", status, out)
	}

	// A past time is refused; the marketer's own time is used.
	status, out = api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true},"send_at":"2026-06-15T11:59:00Z"}`)
	if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["issues"]), "invalid_send_at") {
		t.Fatalf("past time: %d %v", status, out)
	}
	status, out = api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true},"send_at":"2026-06-17T09:30:00+03:00"}`)
	if status != http.StatusOK {
		t.Fatalf("approve scheduled: %d %v", status, out)
	}
	c = campaignOf(t, out)
	if c["status"] != "scheduled" || !sameInstant(c["send_at"], "2026-06-17T06:30:00Z") {
		t.Fatalf("scheduled campaign %v %v", c["status"], c["send_at"])
	}

	// Cancel once; the second time and an unscheduled campaign get 409.
	status, out = api.do("POST", path+"/cancel", "")
	if status != http.StatusOK || campaignOf(t, out)["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", status, out)
	}
	if status, out = api.do("POST", path+"/cancel", ""); status != http.StatusConflict || out["error"] != "not_scheduled" {
		t.Fatalf("cancel twice: %d %v", status, out)
	}
	status, out = api.do("POST", "/api/campaigns", `{"brief":"İstanbul"}`)
	if status != http.StatusCreated || campaignOf(t, out)["send_at"] != nil {
		t.Fatalf("draft without a suggestion: %d %v", status, out)
	}
	if status, out = api.do("POST", idPath(campaignOf(t, out))+"/cancel", ""); status != http.StatusConflict {
		t.Fatalf("cancel a draft: %d %v", status, out)
	}
	if status, _ = api.do("POST", "/api/campaigns/999999/cancel", ""); status != http.StatusNotFound {
		t.Fatalf("cancel missing: %d", status)
	}

	// The dashboard stream of a cancelled campaign ends at once.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api.url+path+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	evs := readEvents(t, resp.Body) // returns when the stream closes
	last := evs[len(evs)-1].Data
	if last["done"] != true || last["status"] != "cancelled" {
		t.Fatalf("last progress event %v", last)
	}
}

// sameInstant compares a JSON time with want as instants; the API writes
// times in the server's zone.
func sameInstant(got any, want string) bool {
	s, ok := got.(string)
	if !ok {
		return false
	}
	a, err1 := time.Parse(time.RFC3339, s)
	b, err2 := time.Parse(time.RFC3339, want)
	return err1 == nil && err2 == nil && a.Equal(b)
}

func TestRetryFailedRoute(t *testing.T) {
	fake := llm.NewFake(llm.Text(caseRuleJSON))
	env := newCampaignEnv(t, fake)
	api := env.api

	status, out := api.do("POST", "/api/campaigns", `{"brief":"İstanbul'da sepeti terk edenler"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	path := idPath(campaignOf(t, out))
	if status, out = api.do("PUT", path+"/content", messageText(t, goodMessage)); status != http.StatusOK {
		t.Fatalf("save content: %d %v", status, out)
	}
	if status, out = api.do("POST", path+"/approve", `{"channels":{"sms":true,"email":true}}`); status != http.StatusOK {
		t.Fatalf("approve: %d %v", status, out)
	}
	// Not completed yet.
	if status, out = api.do("POST", path+"/retry-failed", ""); status != http.StatusConflict || out["error"] != "not_completed" {
		t.Fatalf("retry while approved: %d %v", status, out)
	}

	// Finish by hand: two rows failed, the rest sent.
	ctx := t.Context()
	if _, err := env.pool.Exec(ctx, `UPDATE deliveries SET status = 'sent', sent_at = $1`, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE deliveries SET status = 'failed', attempts = 5, last_error = 'http 500, gave up after 5 attempts'
WHERE id IN (SELECT id FROM deliveries ORDER BY id LIMIT 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE campaigns SET status = 'completed'`); err != nil {
		t.Fatal(err)
	}

	status, out = api.do("POST", path+"/retry-failed", "")
	if status != http.StatusOK || campaignOf(t, out)["status"] != "sending" || fmtAny(out["requeued"]) != "2" {
		t.Fatalf("retry: %d %v", status, out)
	}
	// Sending again: a second click gets 409.
	if status, out = api.do("POST", path+"/retry-failed", ""); status != http.StatusConflict || out["error"] != "not_completed" {
		t.Fatalf("retry twice: %d %v", status, out)
	}
	// Completed with nothing failed.
	if _, err := env.pool.Exec(ctx, `UPDATE deliveries SET status = 'sent', sent_at = $1 WHERE status = 'pending'`, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE campaigns SET status = 'completed'`); err != nil {
		t.Fatal(err)
	}
	if status, out = api.do("POST", path+"/retry-failed", ""); status != http.StatusConflict || out["error"] != "nothing_to_retry" {
		t.Fatalf("retry without failed rows: %d %v", status, out)
	}
	if status, _ = api.do("POST", "/api/campaigns/999999/retry-failed", ""); status != http.StatusNotFound {
		t.Fatalf("retry missing: %d", status)
	}
}
