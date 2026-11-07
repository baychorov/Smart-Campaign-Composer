package httpapi

import (
	"net/http"
	"time"
)

// campaignEvents: GET /api/campaigns/{id}/events. The dashboard stream
// (R-OBS-1, D-STREAM): one "progress" event per interval with a summary
// read from the database, not one event per message. The summary is the
// full state, so a client that reconnects is correct with the next event;
// no replay is needed. The stream ends after the event that reports the
// campaign completed.
func (s *Server) campaignEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	// The first read happens before the stream starts, so an unknown id
	// still gets a JSON 404.
	p, err := s.cfg.Campaigns.Progress(r.Context(), id)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}

	sse := newSSE(w)
	tick := time.NewTicker(s.progressEvery)
	defer tick.Stop()
	for seq := 0; ; seq++ {
		if err := sse.event(seq, sseEvent{Name: "progress", Data: mustJSON(p)}); err != nil {
			return // client went away
		}
		if p.Done {
			return
		}
		select {
		case <-tick.C:
		case <-r.Context().Done():
			return
		}
		next, err := s.cfg.Campaigns.Progress(r.Context(), id)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			// A failed read keeps the last state; the client sees no
			// change until the database answers again.
			s.cfg.Log.Warn("read campaign progress", "campaign_id", id, "err", err)
			continue
		}
		p = next
	}
}
