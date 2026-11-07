package httpapi

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
)

// LLM operations (metric label values).
const (
	opSegment = "segment"
	opContent = "content"
)

// llmMetrics count model calls (D-OBS). They are recorded where the same
// facts are logged: once per segment extraction or content generation.
type llmMetrics struct {
	requests         *prometheus.CounterVec
	repairs          *prometheus.CounterVec
	validationErrors *prometheus.CounterVec
	tokens           *prometheus.CounterVec
	duration         *prometheus.HistogramVec
	ruleStatus       *prometheus.CounterVec
}

// newLLMMetrics registers the metrics on reg; nil keeps them unexported.
func newLLMMetrics(reg prometheus.Registerer) *llmMetrics {
	m := &llmMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_llm_requests_total",
			Help: "Segment extractions and content generations by outcome (ok, invalid, refusal, truncated, timeout, llm_unavailable, llm_error).",
		}, []string{"operation", "outcome"}),
		repairs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_llm_repairs_total",
			Help: "Repair requests sent after invalid model output.",
		}, []string{"operation"}),
		validationErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_llm_validation_errors_total",
			Help: "Validation errors found in model output, by code, across all attempts.",
		}, []string{"operation", "code"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_llm_tokens_total",
			Help: "Tokens used, by direction (input, output).",
		}, []string{"operation", "type"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "scc_llm_operation_duration_seconds",
			Help:    "Duration of one operation, repairs included.",
			Buckets: []float64{1, 2, 4, 8, 15, 30, 60, 120, 180},
		}, []string{"operation"}),
		ruleStatus: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_segment_rule_status_total",
			Help: "Status of extracted rules (ok, partial, unsupported, rejected).",
		}, []string{"status"}),
	}
	if reg != nil {
		reg.MustRegister(m.requests, m.repairs, m.validationErrors, m.tokens, m.duration, m.ruleStatus)
	}
	return m
}

// observe records one finished operation.
func (m *llmMetrics) observe(op, outcome string, attempts []llm.Attempt, repairs int, took time.Duration) {
	m.requests.WithLabelValues(op, outcome).Inc()
	if repairs > 0 {
		m.repairs.WithLabelValues(op).Add(float64(repairs))
	}
	var in, out int64
	for _, a := range attempts {
		in += a.Usage.InputTokens
		out += a.Usage.OutputTokens
		for _, code := range a.IssueCodes {
			m.validationErrors.WithLabelValues(op, code).Inc()
		}
	}
	m.tokens.WithLabelValues(op, "input").Add(float64(in))
	m.tokens.WithLabelValues(op, "output").Add(float64(out))
	m.duration.WithLabelValues(op).Observe(took.Seconds())
}
