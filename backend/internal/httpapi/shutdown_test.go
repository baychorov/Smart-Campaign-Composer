package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
)

// TestShutdownClosesStreams: an open SSE stream must not hold up a graceful
// shutdown. Without CloseStreams, Shutdown waits for the stream until its
// own deadline (10 s in platform.ServeHTTP) and the API exits with an error.
func TestShutdownClosesStreams(t *testing.T) {
	gate := make(chan struct{}) // never opened: the generation stays in progress
	t.Cleanup(func() { close(gate) })
	s := newTestServer(t, gated{gate: gate, inner: llm.NewFake()})
	s.Config.RegisterOnShutdown(s.api.CloseStreams)
	id := s.start(t, "brief", caseRuleJSON)

	resp, err := http.Get(s.URL + "/api/content/generations/" + id + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if _, err := r.ReadString('\n'); err != nil { // the stream is open
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Config.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown with an open stream: %v", err)
	}
}
