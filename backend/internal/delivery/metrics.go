package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the worker's counters (R-OBS-2, D-OBS). Labels are channels,
// results and status classes only: never ids or recipients.
type Metrics struct {
	results          *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
	recordErrors     *prometheus.CounterVec
	leaseLost        *prometheus.CounterVec
}

// NewMetrics registers the worker metrics on reg. A nil reg gives metrics
// that are counted but not exported (tests that do not look at them).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		results: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_delivery_results_total",
			Help: "Processed deliveries by channel and result (sent, retry, throttled, failed, skipped, release).",
		}, []string{"channel", "result"}),
		providerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "scc_provider_request_duration_seconds",
			Help:    "Provider request duration by channel and outcome class (2xx, 4xx, 5xx, error).",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"channel", "class"}),
		recordErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_delivery_record_errors_total",
			Help: "Results that could not be written; the row is resent after its lease.",
		}, []string{"channel"}),
		leaseLost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_delivery_lease_lost_total",
			Help: "Results dropped because another claim took the row after the lease expired.",
		}, []string{"channel"}),
	}
	if reg != nil {
		reg.MustRegister(m.results, m.providerDuration, m.recordErrors, m.leaseLost)
	}
	return m
}

// resultLabel names a decision; a retry after 429 is "throttled".
func resultLabel(d Decision) string {
	if d.Kind == KindRetry && d.Throttled {
		return "throttled"
	}
	return string(d.Kind)
}

// statusClass groups provider outcomes for the latency histogram.
func statusClass(res SendResult) string {
	switch {
	case res.Err != nil:
		return "error"
	case res.StatusCode >= 500:
		return "5xx"
	case res.StatusCode >= 400:
		return "4xx"
	default:
		return "2xx"
	}
}

// queueCollector reports the open rows of the queue on every scrape. It
// reads the database when Prometheus asks, so the value is never stale
// and needs no bookkeeping in the worker.
type queueCollector struct {
	db   *pgxpool.Pool
	log  *slog.Logger
	desc *prometheus.Desc
}

// NewQueueCollector returns a collector for scc_deliveries: open rows
// (pending, sending) per channel and status. Final rows only grow and are
// counted by scc_delivery_results_total.
func NewQueueCollector(db *pgxpool.Pool, log *slog.Logger) prometheus.Collector {
	return &queueCollector{
		db:  db,
		log: log,
		desc: prometheus.NewDesc("scc_deliveries",
			"Open rows in the delivery queue by channel and status.", []string{"channel", "status"}, nil),
	}
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Every series is reported, zero included, so a drained queue shows
	// 0 rather than vanishing from the graph.
	counts := map[[2]string]float64{}
	for _, chn := range Channels {
		counts[[2]string{string(chn), "pending"}] = 0
		counts[[2]string{string(chn), "sending"}] = 0
	}
	// The partial index deliveries_ready_idx covers exactly these rows.
	rows, err := c.db.Query(ctx, `SELECT channel, status, count(*) FROM deliveries
WHERE status IN ('pending', 'sending') GROUP BY channel, status`)
	if err != nil {
		c.log.Warn("queue metrics unavailable", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var chn, status string
		var n int64
		if err := rows.Scan(&chn, &status, &n); err != nil {
			c.log.Warn("queue metrics unavailable", "err", err)
			return
		}
		counts[[2]string{chn, status}] = float64(n)
	}
	if rows.Err() != nil {
		c.log.Warn("queue metrics unavailable", "err", rows.Err())
		return
	}
	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, v, k[0], k[1])
	}
}
