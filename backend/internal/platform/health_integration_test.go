//go:build integration

package platform

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestReadyz: ready while the database answers, 503 without it, and the
// reply never shows connection details.
func TestReadyz(t *testing.T) {
	cfg, err := FromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := OpenDB(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("open db (is `make db-up` running?): %v", err)
	}
	h := Readyz(pool)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("with database: %d %s", rec.Code, rec.Body)
	}

	pool.Close() // stands in for a database outage
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without database: %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, "scc") || strings.Contains(body, "5432") {
		t.Errorf("reply exposes connection details: %q", body)
	}
}
