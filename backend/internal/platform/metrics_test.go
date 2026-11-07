package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPMetricsLabelRoutes(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewHTTPMetrics(reg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/campaigns/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "404" {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/campaigns/{id}/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Flushing must still work through the recorder (SSE).
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the metrics wrapper: %v", err)
		}
	})
	h := m.Wrap(mux)

	for _, path := range []string{"/api/campaigns/1", "/api/campaigns/2", "/api/campaigns/3", "/api/campaigns/404", "/api/campaigns/7/events", "/secret/+905551112233"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	const route = "GET /api/campaigns/{id}"
	for _, tt := range []struct {
		route, code string
		want        float64
	}{
		{route, "200", 3},
		{route, "404", 1},
		{"GET /api/campaigns/{id}/events", "200", 1},
		{"unmatched", "404", 1},
	} {
		if got := testutil.ToFloat64(m.requests.WithLabelValues(tt.route, tt.code)); got != tt.want {
			t.Errorf("requests{%s,%s} = %v, want %v", tt.route, tt.code, got, tt.want)
		}
	}
	// One series per route and code, whatever the ids.
	if n := testutil.CollectAndCount(m.requests); n != 4 {
		t.Errorf("%d request series, want 4", n)
	}
	// The stream is counted but its duration is not observed.
	if n := testutil.CollectAndCount(m.duration); n != 2 {
		t.Errorf("%d duration series, want 2 (route + unmatched, no stream)", n)
	}

	rec := httptest.NewRecorder()
	MetricsHandler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := rec.Body.String()
	if !strings.Contains(out, `scc_http_requests_total{code="200",route="GET /api/campaigns/{id}"} 3`) {
		t.Errorf("metrics output lacks the request counter:\n%s", out)
	}
	for _, leak := range []string{"/api/campaigns/1", "905551112233"} {
		if strings.Contains(out, leak) {
			t.Errorf("metrics output contains %q", leak)
		}
	}
}

func TestNewRegistryHasRuntimeMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	MetricsHandler(NewRegistry()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Fatal("Go runtime metrics missing")
	}
}
