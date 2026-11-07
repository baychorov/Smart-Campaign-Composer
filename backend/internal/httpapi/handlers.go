// Package httpapi serves the JSON and SSE API used by the frontend.
// Handlers only decode, call the business packages and encode; they hold
// no business rules of their own.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Config wires the server. Longest returns the longest value of each
// personalisation variable (campaign.LongestValues in production).
type Config struct {
	LLM         llm.Client
	LLMProvider string // "mock" or "anthropic", shown as a badge in the UI
	Longest     func(ctx context.Context) (content.Values, error)
	Log         *slog.Logger
	// GenerationTimeout bounds one content generation, repairs included.
	// Zero means 3 minutes.
	GenerationTimeout time.Duration
	Now               func() time.Time // nil means time.Now
}

// Server holds the handlers' dependencies.
type Server struct {
	cfg  Config
	gens *registry
	// pingEvery is the SSE keep-alive interval.
	pingEvery time.Duration
}

const (
	maxBodyBytes  = 64 << 10
	generationTTL = 30 * time.Minute
)

// New builds a Server.
func New(cfg Config) *Server {
	if cfg.GenerationTimeout == 0 {
		cfg.GenerationTimeout = 3 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{cfg: cfg, gens: newRegistry(generationTTL, cfg.Now), pingEvery: 15 * time.Second}
}

// Register adds the API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("POST /api/content/generations", s.startGeneration)
	mux.HandleFunc("GET /api/content/generations/{id}/events", s.generationEvents)
	mux.HandleFunc("POST /api/content/validate", s.validateContent)
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"llm_provider": s.cfg.LLMProvider})
}

// generationRequest is the body of POST /api/content/generations. The rule
// is the one the marketer approved; it is validated again here because the
// client is not trusted.
type generationRequest struct {
	Brief string       `json:"brief"`
	Rule  segment.Rule `json:"rule"`
}

func (s *Server) startGeneration(w http.ResponseWriter, r *http.Request) {
	var req generationRequest
	if !decodeBody(w, r, &req) {
		return
	}
	brief, err := llm.CheckBrief(req.Brief)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_brief", "Brief boş olamaz ve en fazla "+strconv.Itoa(llm.MaxBriefRunes)+" karakter olabilir.")
		return
	}
	rule := segment.Validate(req.Rule)
	if !rule.Approvable() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "invalid_rule",
			"message": "Kural geçerli değil veya bir kitle tanımlamıyor.",
			"issues":  nonNil(rule.Errors),
		})
		return
	}
	longest, err := s.cfg.Longest(r.Context())
	if err != nil {
		s.cfg.Log.Error("load longest values", "err", err)
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "Veritabanına şu anda ulaşılamıyor.")
		return
	}

	in := llm.ContentInput{
		Brief:    brief,
		Audience: segment.Summarize(rule.Rule).String(),
		Notes:    rule.Rule.Notes,
	}
	id, g := s.gens.create()
	// The generation outlives this request: the client follows it through
	// the events endpoint, and may reconnect.
	go s.runGeneration(id, g, in, longest)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":         id,
		"events_url": "/api/content/generations/" + id + "/events",
	})
}

// SSE event names (D-STREAM).
var deltaEventNames = map[string]string{
	content.FieldSMS:          "sms.delta",
	content.FieldEmailSubject: "email.subject.delta",
	content.FieldEmailBody:    "email.body.delta",
}

const eventFailed = "failed"

func (s *Server) runGeneration(id string, g *generation, in llm.ContentInput, longest content.Values) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.GenerationTimeout)
	defer cancel()
	start := s.cfg.Now()

	res, err := llm.GenerateContent(ctx, s.cfg.LLM, in, longest, func(e llm.ContentEvent) {
		g.append(toSSE(e), e.Type == llm.ContentValidated || e.Type == llm.ContentInvalid)
	})

	outcome := "validated"
	switch {
	case err == nil:
	case errors.Is(err, llm.ErrInvalidOutput):
		outcome = "invalid" // the invalid event is already sent
	default:
		code, msg := failure(err)
		outcome = code
		g.append(sseEvent{Name: eventFailed, Data: mustJSON(map[string]string{"code": code, "message": msg})}, true)
	}
	// The brief and the text are not logged: they may contain personal data.
	usage := res.Usage()
	attrs := []any{"id", id, "outcome", outcome, "repairs", res.Repairs, "attempts", len(res.Attempts),
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "duration", s.cfg.Now().Sub(start)}
	if err != nil && outcome != "invalid" {
		s.cfg.Log.Warn("content generation failed", append(attrs, "err", err)...)
		return
	}
	s.cfg.Log.Info("content generation finished", attrs...)
}

func toSSE(e llm.ContentEvent) sseEvent {
	switch e.Type {
	case llm.ContentDelta:
		return sseEvent{Name: deltaEventNames[e.Field], Data: mustJSON(map[string]string{"text": e.Text})}
	case llm.ContentValidated:
		return sseEvent{Name: string(e.Type), Data: mustJSON(map[string]any{"message": e.Result.Message, "stats": e.Result.Stats})}
	case llm.ContentInvalid:
		return sseEvent{Name: string(e.Type), Data: mustJSON(map[string]any{"issues": nonNil(e.Issues)})}
	default: // started, reset
		return sseEvent{Name: string(e.Type), Data: []byte("{}")}
	}
}

// failure maps a generation error to a code and a Turkish message for the
// marketer.
func failure(err error) (code, msg string) {
	var refusal *llm.RefusalError
	var apiErr *llm.APIError
	switch {
	case errors.As(err, &refusal):
		msg = "Model bu isteği yanıtlamayı reddetti."
		if refusal.Refusal.Explanation != "" {
			msg += " " + refusal.Refusal.Explanation
		}
		return "refusal", msg
	case errors.Is(err, llm.ErrTruncated):
		return "truncated", "Model yanıtı uzunluk sınırında kesildi."
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "İçerik üretimi zaman aşımına uğradı."
	case errors.As(err, &apiErr):
		return "llm_unavailable", fmt.Sprintf("Claude'a şu anda ulaşılamıyor (HTTP %d).", apiErr.StatusCode)
	default:
		return "llm_error", "İçerik üretilemedi."
	}
}

func (s *Server) generationEvents(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gens.get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Üretim bulunamadı veya süresi doldu.")
		return
	}
	next := 0
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.Atoi(last); err == nil && n >= 0 {
			next = n + 1
		}
	}

	sse := newSSE(w)
	ping := time.NewTicker(s.pingEvery)
	defer ping.Stop()
	for {
		evs, done, changed := g.since(next)
		for _, e := range evs {
			if err := sse.event(next, e); err != nil {
				return // client went away
			}
			next++
		}
		if done {
			return
		}
		select {
		case <-changed:
		case <-ping.C:
			if err := sse.ping(); err != nil {
				return
			}
		case <-r.Context().Done():
			// The client disconnected. The generation keeps running and can
			// be followed again with Last-Event-ID.
			return
		}
	}
}

// validateContent checks a marketer-edited message with the same rules as
// generated copy (D-CONTENT).
func (s *Server) validateContent(w http.ResponseWriter, r *http.Request) {
	var m content.Message
	if !decodeBody(w, r, &m) {
		return
	}
	longest, err := s.cfg.Longest(r.Context())
	if err != nil {
		s.cfg.Log.Error("load longest values", "err", err)
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "Veritabanına şu anda ulaşılamıyor.")
		return
	}
	res := content.Validate(m, longest)
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":   res.Valid(),
		"message": res.Message,
		"errors":  nonNil(res.Errors),
		"stats":   res.Stats,
	})
}

// decodeBody reads a size-limited JSON body strictly. It writes the error
// response itself and reports whether decoding succeeded.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "İstek gövdesi geçerli bir JSON değil: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal event: %v", err)) // programmer error: plain types only
	}
	return b
}

// nonNil turns a nil slice into an empty one, so JSON has [] not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
