package prometheus

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/klauspost/compress/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

// Remote write 1.0 field numbers from prometheus/prompb types.proto and
// remote.proto.
const (
	fieldWriteTimeseries = 1 // WriteRequest.timeseries
	fieldSeriesLabels    = 1 // TimeSeries.labels
	fieldSeriesSamples   = 2 // TimeSeries.samples
	fieldLabelName       = 1 // Label.name
	fieldLabelValue      = 2 // Label.value
	fieldSampleValue     = 1 // Sample.value, double
	fieldSampleTimestamp = 2 // Sample.timestamp, int64 milliseconds
)

// nameLabel is the label remote write carries the metric name in.
const nameLabel = "__name__"

// label is one name and value pair of a series.
type label struct{ name, value string }

// series is the samples of one label set, ready to encode.
type series struct {
	labels  []label
	times   []int64
	values  []float64
	byTime  map[int64]int
	ordered bool
}

// Write sends numeric points by remote write. Text-only points are skipped
// because Prometheus stores numbers only.
func (d *Driver) Write(ctx context.Context, points []tsdb.Point) error {
	if !d.cfg.WriteEnabled {
		return tsdb.Unsupportedf("writing is turned off for this connection")
	}
	ctx, cancel := context.WithTimeout(ctx, d.QueryTimeout())
	defer cancel()
	return remoteWrite(ctx, d.client, d.cfg, points)
}

// remoteWrite posts points as one snappy-compressed WriteRequest. No points
// sends an empty request, which servers accept without storing anything.
func remoteWrite(ctx context.Context, client *http.Client, cfg Config, points []tsdb.Point) error {
	body := snappy.Encode(nil, encodeWriteRequest(group(points)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.writeURL(), bytes.NewReader(body))
	if err != nil {
		return tsdb.Rejectedf("build request: %v", err)
	}
	authorize(req, cfg)
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	_, err = tsdb.Do(client, req)
	return tsdb.WriteError(err)
}

// group collects points into series by sanitized label set, with labels
// sorted by name and samples by time, as remote write requires. A repeated
// timestamp keeps the last value, since Prometheus rejects a whole batch
// holding two different values for one series and time.
func group(points []tsdb.Point) []*series {
	byKey := map[string]*series{}
	var out []*series
	for _, p := range points {
		if p.Value == nil {
			continue
		}
		labels := seriesLabels(p)
		var key strings.Builder
		for _, l := range labels {
			key.WriteString(l.name)
			key.WriteString("\xff")
			key.WriteString(l.value)
			key.WriteString("\xff")
		}
		s := byKey[key.String()]
		if s == nil {
			s = &series{labels: labels, byTime: map[int64]int{}, ordered: true}
			byKey[key.String()] = s
			out = append(out, s)
		}
		if i, ok := s.byTime[p.Time]; ok {
			s.values[i] = *p.Value
			continue
		}
		if n := len(s.times); n > 0 && s.times[n-1] > p.Time {
			s.ordered = false
		}
		s.byTime[p.Time] = len(s.times)
		s.times = append(s.times, p.Time)
		s.values = append(s.values, *p.Value)
	}
	for _, s := range out {
		if !s.ordered {
			sort.Sort(byTime{s})
		}
	}
	return out
}

// byTime sorts a series' samples by timestamp.
type byTime struct{ s *series }

func (b byTime) Len() int           { return len(b.s.times) }
func (b byTime) Less(i, j int) bool { return b.s.times[i] < b.s.times[j] }
func (b byTime) Swap(i, j int) {
	b.s.times[i], b.s.times[j] = b.s.times[j], b.s.times[i]
	b.s.values[i], b.s.values[j] = b.s.values[j], b.s.values[i]
}

// seriesLabels returns a point's labels in remote write form: __name__ plus
// sanitized names, sorted. Empty values are dropped, as Prometheus treats
// them as absent, and of names that sanitize alike or collide with
// __name__, the first in sorted order wins, as duplicates fail the batch.
func seriesLabels(p tsdb.Point) []label {
	keys := make([]string, 0, len(p.Labels))
	for k, v := range p.Labels {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	seen := map[string]bool{nameLabel: true}
	labels := []label{{name: nameLabel, value: tsdb.SanitizeMetric(p.Metric)}}
	for _, k := range keys {
		name := tsdb.SanitizeLabel(k)
		if seen[name] {
			continue
		}
		seen[name] = true
		labels = append(labels, label{name: name, value: p.Labels[k]})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].name < labels[j].name })
	return labels
}

// encodeWriteRequest encodes a prometheus.WriteRequest.
func encodeWriteRequest(list []*series) []byte {
	var out, ts, msg []byte
	for _, s := range list {
		ts = ts[:0]
		for _, l := range s.labels {
			msg = msg[:0]
			msg = protowire.AppendTag(msg, fieldLabelName, protowire.BytesType)
			msg = protowire.AppendString(msg, l.name)
			msg = protowire.AppendTag(msg, fieldLabelValue, protowire.BytesType)
			msg = protowire.AppendString(msg, l.value)
			ts = protowire.AppendTag(ts, fieldSeriesLabels, protowire.BytesType)
			ts = protowire.AppendBytes(ts, msg)
		}
		for i, t := range s.times {
			msg = msg[:0]
			msg = protowire.AppendTag(msg, fieldSampleValue, protowire.Fixed64Type)
			msg = protowire.AppendFixed64(msg, math.Float64bits(s.values[i]))
			msg = protowire.AppendTag(msg, fieldSampleTimestamp, protowire.VarintType)
			msg = protowire.AppendVarint(msg, uint64(t))
			ts = protowire.AppendTag(ts, fieldSeriesSamples, protowire.BytesType)
			ts = protowire.AppendBytes(ts, msg)
		}
		out = protowire.AppendTag(out, fieldWriteTimeseries, protowire.BytesType)
		out = protowire.AppendBytes(out, ts)
	}
	return out
}
