package tsdb

import (
	"slices"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Kinds that answer queries, and kinds that accept writes. Home Assistant is
// readable through its existing connection kind.
const (
	ReadableKinds = "prometheus,influxdb,postgres,homeassistant"
	WritableKinds = "prometheus,influxdb,postgres"
)

// TimeoutField is the query timeout setting every kind shares.
func TimeoutField() flow.FieldSpec {
	return flow.FieldSpec{
		ID: "timeout_s", Label: "Query timeout (seconds)", Type: flow.FieldNumber,
		Placeholder: "30", Help: "Queries running longer are cancelled.",
	}
}

// WriteFields are the history writer settings of a writable kind. showIf
// names the boolean field that turns writing on, or is empty when the kind
// always writes.
func WriteFields(showIf string, heartbeatDefault string) []flow.FieldSpec {
	var cond *flow.ShowIf
	if showIf != "" {
		cond = &flow.ShowIf{Field: showIf, Equals: true}
	}
	return []flow.FieldSpec{
		{
			ID: "heartbeat_s", Label: "Heartbeat (seconds)", Type: flow.FieldNumber,
			Placeholder: heartbeatDefault, ShowIf: cond,
			Help: "Re-send each forwarded series' last value this often. 0 turns it off.",
		},
		{
			ID: "buffer_mib", Label: "Buffer size (MiB)", Type: flow.FieldNumber,
			Placeholder: "64", ShowIf: cond,
			Help: "Disk space for points waiting while the database is unreachable. The oldest points are discarded beyond it.",
		},
	}
}

// IsReadable reports whether a connection kind answers time-series queries.
func IsReadable(kind string) bool {
	return kind != "" && slices.Contains(strings.Split(ReadableKinds, ","), kind)
}

// IsWritable reports whether a connection kind accepts history points.
func IsWritable(kind string) bool {
	return kind != "" && slices.Contains(strings.Split(WritableKinds, ","), kind)
}
