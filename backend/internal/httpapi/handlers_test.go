package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/campaign"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

const longestName = "Muhammed Abdurrahman Şahabettin"

var caseRuleJSON = `{"status":"ok","audience_scope":"filtered","common":[{"type":"attribute","field":"city","op":"in","values":["İstanbul"]},{"type":"event","event":"add_to_cart","occurred":true,"within_days":30},{"type":"event","event":"purchase","occurred":false,"within_days":30}],"groups":[],"assumptions":[],"unsupported":[],"notes":["Samimi dil"]}`

var goodMessage = content.Message{
	SMS:          "Merhaba {{first_name}}! Sepetindeki ürünler seni bekliyor: SEPET10 ile %10 indirim.",
	EmailSubject: "{{first_name}}, sepetin seni bekliyor",
	EmailBody:    "Merhaba {{first_name}},\n\n\"SEPET10\" koduyla %10 indirim.",
}

func messageText(t *testing.T, m content.Message) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// chunked splits s every n bytes, cutting multi-byte runes on purpose.
func chunked(s string, n int) []string {
	var out []string
	for i := 0; i < len(s); i += n {
		out = append(out, s[i:min(i+n, len(s))])
	}
	return out
}

type testServer struct {
	*httptest.Server
	api  *Server
	logs *logBuffer
}

// logBuffer is a log sink that is safe to read while the generation
// goroutine writes to it, and lets a test wait for the next line.
type logBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	wrote chan struct{}
}

func newLogBuffer() *logBuffer { return &logBuffer{wrote: make(chan struct{}, 100)} }

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	b.wrote <- struct{}{}
	return n, err
}

// wait blocks until at least one line was written, then returns the log.
func (b *logBuffer) wait(t *testing.T) string {
	t.Helper()
	select {
	case <-b.wrote:
	case <-time.After(10 * time.Second):
		t.Fatal("no log line written")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestServer(t *testing.T, client llm.Client) *testServer {
	t.Helper()
	logs := newLogBuffer()
	api := New(Config{
		LLM:               client,
		LLMProvider:       "mock",
		Log:               slog.New(slog.NewJSONHandler(logs, nil)),
		GenerationTimeout: 10 * time.Second,
	})
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &testServer{Server: srv, api: api, logs: logs}
}

func (s *testServer) post(t *testing.T, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(s.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	return resp, out
}

// start launches a generation the way the campaign route does, without a
// database: the input is built from the rule like campaign.GenerationInput.
func (s *testServer) start(t *testing.T, brief, rule string) string {
	t.Helper()
	var r segment.Rule
	if err := json.Unmarshal([]byte(rule), &r); err != nil {
		t.Fatal(err)
	}
	v := segment.Validate(r)
	if !v.Approvable() {
		t.Fatalf("test rule not approvable: %v", v.Errors)
	}
	in := campaign.ContentInput(campaign.Campaign{Brief: brief, Rule: v.Rule})
	return s.api.launchGeneration(in, content.Values{content.VarFirstName: longestName, content.VarCity: "Afyonkarahisar"})
}

type event struct {
	ID   string
	Name string
	Data map[string]any
}

// readEvents reads an SSE stream until the server closes it.
func readEvents(t *testing.T, body io.Reader) []event {
	t.Helper()
	var out []event
	var cur event
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.Name != "" {
				out = append(out, cur)
			}
			cur = event{}
		case strings.HasPrefix(line, ":"): // comment (ping)
		case strings.HasPrefix(line, "id: "):
			cur.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data); err != nil {
				t.Fatalf("event data is not JSON: %q", line)
			}
		default:
			t.Fatalf("unexpected SSE line %q", line)
		}
	}
	return out
}

func (s *testServer) events(t *testing.T, id, lastEventID string) []event {
	t.Helper()
	return streamEvents(t, s.URL, id, lastEventID)
}

// streamEvents reads the whole event stream of generation id from the API
// at baseURL.
func streamEvents(t *testing.T, baseURL, id, lastEventID string) []event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/content/generations/"+id+"/events", nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return readEvents(t, resp.Body)
}

func names(evs []event) []string {
	var out []string
	for _, e := range evs {
		if len(out) > 0 && out[len(out)-1] == e.Name && strings.HasSuffix(e.Name, ".delta") {
			continue // collapse a run of deltas of the same field
		}
		out = append(out, e.Name)
	}
	return out
}

// shown rebuilds what the UI displays from the deltas, honouring resets.
func shown(evs []event) map[string]string {
	out := map[string]string{}
	for _, e := range evs {
		switch e.Name {
		case "reset":
			out = map[string]string{}
		case "sms.delta", "email.subject.delta", "email.body.delta":
			out[e.Name] += e.Data["text"].(string)
		}
	}
	return out
}

func TestGenerationStream(t *testing.T) {
	fake := llm.NewFake(llm.FakeResponse{Chunks: chunked(messageText(t, goodMessage), 5)})
	s := newTestServer(t, fake)
	id := s.start(t, "İstanbul'daki sepet terk edenlere hatırlatma.", caseRuleJSON)

	evs := s.events(t, id, "")
	// Deltas follow the field order of the JSON the model wrote (here the
	// struct order of json.Marshal).
	if got := names(evs); !slices.Equal(got, []string{"started", "sms.delta", "email.subject.delta", "email.body.delta", "validated"}) {
		t.Fatalf("event names %v", got)
	}
	for i, e := range evs {
		if e.ID != strconv.Itoa(i) {
			t.Fatalf("event %d has id %q", i, e.ID)
		}
	}
	last := evs[len(evs)-1]
	msg := last.Data["message"].(map[string]any)
	ui := shown(evs)
	if ui["sms.delta"] != msg["sms"] || ui["email.subject.delta"] != msg["email_subject"] || ui["email.body.delta"] != msg["email_body"] {
		t.Fatalf("streamed text differs from validated text:\n%v\n%v", ui, msg)
	}
	stats := last.Data["stats"].(map[string]any)["sms"].(map[string]any)
	if stats["limit"] != float64(160) || stats["worst_case_runes"].(float64) <= stats["runes"].(float64) {
		t.Errorf("sms stats %v", stats)
	}
	if strings.Contains(fmtAny(last.Data), longestName) {
		t.Error("the validated event must not expose customer values")
	}

	// The model got the rule summary and notes, not the raw rule.
	prompt := fake.Calls()[0].Prompt
	for _, s := range []string{"şehri İstanbul olan", "son 30 günde alışveriş yapmamış", "Samimi dil", "{{first_name}}: 31"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	if strings.Contains(prompt, longestName) {
		t.Error("prompt contains a customer name")
	}

	// The log line has usage but no brief or text. It is written after the
	// terminal event, so wait for it.
	logs := s.logs.wait(t)
	if !strings.Contains(logs, `"outcome":"validated"`) || strings.Contains(logs, "sepet terk") || strings.Contains(logs, "SEPET10") {
		t.Errorf("log line: %s", logs)
	}
}

func fmtAny(v any) string { b, _ := json.Marshal(v); return string(b) }

// TestGenerationReplayAndResume: a client that connects after the end gets
// every event; Last-Event-ID resumes after the given id.
func TestGenerationReplayAndResume(t *testing.T) {
	s := newTestServer(t, llm.NewFake(llm.Text(messageText(t, goodMessage))))
	id := s.start(t, "brief", caseRuleJSON)
	all := s.events(t, id, "")
	if len(all) < 3 {
		t.Fatalf("events %v", names(all))
	}
	again := s.events(t, id, "")
	if len(again) != len(all) {
		t.Fatalf("replay has %d events, first read %d", len(again), len(all))
	}
	resumed := s.events(t, id, "0")
	if len(resumed) != len(all)-1 || resumed[0].ID != "1" {
		t.Fatalf("resume after 0: %d events, first id %q", len(resumed), resumed[0].ID)
	}
}

// gated blocks every Stream call until gate is closed, so a test can
// connect to the event stream while the generation is in progress.
type gated struct {
	gate  chan struct{}
	inner llm.Client
}

func (g gated) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
	return g.inner.Stream(ctx, req, onDelta)
}

// TestGenerationLive: events reach a connected client as they happen, not
// only when the generation ends (each event is flushed).
func TestGenerationLive(t *testing.T) {
	gate := make(chan struct{})
	s := newTestServer(t, gated{gate: gate, inner: llm.NewFake(llm.Text(messageText(t, goodMessage)))})
	id := s.start(t, "brief", caseRuleJSON)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/api/content/generations/"+id+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)

	// "started" arrives while the model call is still blocked.
	var head []string
	for len(head) < 3 {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, head)
		}
		head = append(head, strings.TrimSpace(line))
	}
	if head[1] != "event: started" {
		t.Fatalf("first event %q", head)
	}
	close(gate)
	rest := readEvents(t, r)
	if rest[len(rest)-1].Name != "validated" {
		t.Fatalf("stream ended with %v", names(rest))
	}
}

func TestGenerationTerminalEvents(t *testing.T) {
	t.Run("refusal becomes a failed event", func(t *testing.T) {
		fake := llm.NewFake(llm.FakeResponse{StopReason: llm.StopRefusal, Refusal: &llm.Refusal{Category: "cyber", Explanation: "Politika."}})
		s := newTestServer(t, fake)
		evs := s.events(t, s.start(t, "brief", caseRuleJSON), "")
		last := evs[len(evs)-1]
		if last.Name != "failed" || last.Data["code"] != "refusal" || !strings.Contains(last.Data["message"].(string), "Politika.") {
			t.Fatalf("last event %+v", last)
		}
	})

	t.Run("invalid after repairs", func(t *testing.T) {
		bad := goodMessage
		bad.SMS = "{{soyad}}"
		var replies []llm.FakeResponse
		for range llm.MaxRepairs + 1 {
			replies = append(replies, llm.Text(messageText(t, bad)))
		}
		s := newTestServer(t, llm.NewFake(replies...))
		evs := s.events(t, s.start(t, "brief", caseRuleJSON), "")
		got := names(evs)
		if got[len(got)-1] != "invalid" || slices.Contains(got, "validated") || !slices.Contains(got, "reset") {
			t.Fatalf("events %v", got)
		}
		issues := evs[len(evs)-1].Data["issues"].([]any)
		if issues[0].(map[string]any)["code"] != content.CodeUnknownVariable {
			t.Errorf("issues %v", issues)
		}
	})

	t.Run("api error", func(t *testing.T) {
		s := newTestServer(t, llm.NewFake(llm.FakeResponse{Err: &llm.APIError{StatusCode: 529}}))
		evs := s.events(t, s.start(t, "brief", caseRuleJSON), "")
		if last := evs[len(evs)-1]; last.Name != "failed" || last.Data["code"] != "llm_unavailable" {
			t.Fatalf("last event %+v", last)
		}
	})
}

func TestEventsUnknownID(t *testing.T) {
	s := newTestServer(t, llm.NewFake())
	resp, err := http.Get(s.URL + "/api/content/generations/nope/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestRegistryDropsExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := newRegistry(time.Minute, func() time.Time { return now })
	_, done := r.create()
	done.append(sseEvent{Name: "validated"}, true)
	_, _ = r.create() // running: never dropped
	now = now.Add(2 * time.Minute)
	_, _ = r.create()
	if r.len() != 2 {
		t.Fatalf("registry has %d generations, want 2", r.len())
	}
}

func TestInfo(t *testing.T) {
	s := newTestServer(t, llm.NewFake())
	resp, err := http.Get(s.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["llm_provider"] != "mock" {
		t.Fatalf("info %v", out)
	}
}
