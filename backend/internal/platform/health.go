package platform

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// readyTimeout bounds the readiness check's database ping.
const readyTimeout = 2 * time.Second

// Readyz is the readiness handler (R-OBS-3): the process can serve
// requests, which needs the database. /healthz only says the process is up;
// an orchestrator restarts on a failing /healthz but only stops routing
// traffic on a failing /readyz, so a database outage does not cause a
// restart loop.
func Readyz(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// The error is not shown: it can name hosts and users.
		if err := db.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: database unreachable\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}
