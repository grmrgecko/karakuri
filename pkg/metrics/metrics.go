// Package metrics defines the application-wide Prometheus metrics, exposed
// at /metrics alongside the standard Go and process collectors. All metrics
// are auto-registered on the default registry at init time. Flow labels use
// the flow slug to keep label cardinality bounded by the number of flows.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RunsTotal counts finished runs by flow slug and terminal status
	// (succeeded, failed, or canceled). Incremented by the engine when a run
	// settles.
	RunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_runs_total",
			Help: "Total finished flow runs by flow slug and terminal status.",
		},
		[]string{"flow", "status"},
	)

	// NodeRetriesTotal counts retry attempts scheduled by flow slug and node
	// type. Incremented by the engine each time a failed node waits for
	// another attempt.
	NodeRetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_node_retries_total",
			Help: "Total node retries scheduled by flow slug and node type.",
		},
		[]string{"flow", "node_type"},
	)

	// ActiveRuns tracks the number of runs currently executing in memory.
	ActiveRuns = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "karakuri_active_runs",
			Help: "Number of flow runs currently executing.",
		},
	)

	// Connections tracks the number of enabled integration connections per
	// driver state (connecting, connected, or error). Maintained by the
	// connection manager on every status transition.
	Connections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "karakuri_connections",
			Help: "Number of integration connections by driver state.",
		},
		[]string{"state"},
	)

	// ConnectionReconnectsTotal counts session reestablishments by driver
	// kind. A steadily climbing count means the link is flapping even while
	// the connection reports itself connected.
	ConnectionReconnectsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_connection_reconnects_total",
			Help: "Total connection reestablishments by driver kind.",
		},
		[]string{"kind"},
	)

	// MessagesDroppedTotal counts inbound messages discarded because a
	// connection's delivery queue was saturated. Any increase is trigger data
	// loss caused by flows that cannot keep up with the message rate.
	MessagesDroppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_messages_dropped_total",
			Help: "Total inbound messages dropped because the delivery queue was full.",
		},
		[]string{"kind"},
	)

	// AITokensTotal counts tokens spent through AI provider connections by
	// connection ID, model, and direction (input or output). Run history
	// keeps only the last run per flow, so per-run usage metadata cannot
	// answer what a month of automation cost; this can. Recorded by the AI
	// driver on every chat call, whichever node made it.
	AITokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_ai_tokens_total",
			Help: "Total AI provider tokens by connection, model, and direction.",
		},
		[]string{"connection", "model", "direction"},
	)

	// AIRequestsTotal counts AI provider calls by connection, model, call
	// kind (chat or embed), and outcome. A climbing error count names the
	// connection going bad even while retries keep flows green.
	AIRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_ai_requests_total",
			Help: "Total AI provider calls by connection, model, kind, and outcome.",
		},
		[]string{"connection", "model", "kind", "status"},
	)

	// HistoryPointsTotal counts points handled by the history writers by
	// connection ID and result: written, discarded (buffer full or
	// unreadable), rejected (refused by the destination), or skipped
	// (connection or writing disabled).
	HistoryPointsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "karakuri_history_points_total",
			Help: "History points handled by connection and result.",
		},
		[]string{"connection", "result"},
	)

	// HistoryBufferedPoints is the number of points a connection's writer
	// holds on disk awaiting delivery.
	HistoryBufferedPoints = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "karakuri_history_buffered_points",
			Help: "History points buffered on disk by connection.",
		},
		[]string{"connection"},
	)

	// HistoryBufferedBytes is the disk space a connection's history buffer
	// uses.
	HistoryBufferedBytes = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "karakuri_history_buffered_bytes",
			Help: "Disk space of buffered history points by connection.",
		},
		[]string{"connection"},
	)
)
