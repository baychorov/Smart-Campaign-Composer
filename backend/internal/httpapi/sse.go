package httpapi

import (
	"fmt"
	"net/http"
	"time"
)

// sseEvent is one Server-Sent Event. Data is a single-line JSON document
// (encoding/json escapes newlines), so it fits on one "data:" line.
type sseEvent struct {
	Name string
	Data []byte
}

// sseWriter writes events to a streaming response. Each write is flushed
// at once, otherwise the client would see the deltas in one burst.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// newSSE sets the stream headers. It clears the write deadline for this
// response, so a server-wide WriteTimeout cannot cut a long stream
// (D-STREAM). X-Accel-Buffering stops nginx from buffering the stream.
func newSSE(w http.ResponseWriter) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // not supported by every writer (tests); harmless
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, rc: rc}
}

// event writes one event with its id. Clients send the last id they saw
// in Last-Event-ID when reconnecting.
func (s *sseWriter) event(id int, e sseEvent) error {
	if _, err := fmt.Fprintf(s.w, "id: %d\nevent: %s\ndata: %s\n\n", id, e.Name, e.Data); err != nil {
		return err
	}
	return s.rc.Flush()
}

// ping writes a comment line that keeps idle proxies from closing the
// connection.
func (s *sseWriter) ping() error {
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err != nil {
		return err
	}
	return s.rc.Flush()
}
