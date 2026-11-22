//go:build karakuri_tsdb

// Command seed writes the fixture series dashboard chart checks draw to the
// containers in test/tsdb/compose.yaml: a sine wave per room, a door that
// opens and closes, and hourly open/high/low/close prices. InfluxDB 2 and
// TimescaleDB get seven days; Prometheus, which refuses samples older than
// its head block, gets the last 50 minutes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/influxdb"
	"github.com/grmrgecko/karakuri/pkg/integrations/postgresql"
	"github.com/grmrgecko/karakuri/pkg/integrations/prometheus"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"go.uber.org/zap"
)

func model(kind string, cfg map[string]any) integrations.ConnModel {
	b, _ := json.Marshal(cfg)
	return integrations.ConnModel{ID: uuid.New(), Kind: kind, Config: b}
}

var deps = integrations.Deps{Logger: zap.NewNop(), PublishStatus: func(integrations.Status) {}}

// series builds the fixture points between from and to at step.
func series(from, to time.Time, step time.Duration) []tsdb.Point {
	var out []tsdb.Point
	door := false
	for t := from; t.Before(to); t = t.Add(step) {
		ms := t.UnixMilli()
		hours := float64(t.Unix()) / 3600
		for i, room := range []string{"kitchen", "office"} {
			v := 20 + 3*math.Sin(2*math.Pi*hours/24+float64(i)) + float64(i)
			out = append(out, tsdb.Point{Time: ms, Metric: "seed_wave", Labels: map[string]string{"room": room}, Value: tsdb.Float(math.Round(v*100) / 100)})
		}
		if int(hours*4)%7 == 0 {
			door = !door
		}
		d := 0.0
		state := "closed"
		if door {
			d, state = 1, "open"
		}
		out = append(out, tsdb.Point{Time: ms, Metric: "seed_door", Labels: map[string]string{"door": "front"}, Value: tsdb.Float(d)})
		out = append(out, tsdb.Point{Time: ms, Metric: "seed_door_state", Labels: map[string]string{"door": "front"}, State: tsdb.Text(state)})
		if t.Minute() == 0 && t.Second() == 0 {
			base := 100 + 10*math.Sin(hours/12)
			for name, v := range map[string]float64{"open": base, "high": base + 3, "low": base - 2.5, "close": base + math.Sin(hours)} {
				out = append(out, tsdb.Point{Time: ms, Metric: "seed_price", Labels: map[string]string{"field": name}, Value: tsdb.Float(math.Round(v*100) / 100)})
			}
		}
	}
	return out
}

func write(ctx context.Context, name string, sink tsdb.Sink, points []tsdb.Point) error {
	for len(points) > 0 {
		n := min(len(points), 5000)
		if err := sink.Write(ctx, points[:n]); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		points = points[n:]
	}
	fmt.Printf("seeded %s\n", name)
	return nil
}

func run() error {
	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)
	week := series(now.Add(-7*24*time.Hour).Truncate(time.Hour), now, 5*time.Minute)

	influx, err := influxdb.New(model(influxdb.Kind, map[string]any{
		"version": "2", "url": "http://localhost:19087", "org": "karakuri", "bucket": "karakuri",
		"token": "karakuri-test-token", "write_enabled": true,
	}), deps)
	if err != nil {
		return err
	}
	if err := write(ctx, "InfluxDB 2", influx, week); err != nil {
		return err
	}

	pg, err := postgresql.New(model(postgresql.Kind, map[string]any{
		"host": "localhost", "port": 19432, "database": "karakuri", "username": "karakuri",
		"password": "karakuri-test", "sslmode": "disable", "write_enabled": true,
	}), deps)
	if err != nil {
		return err
	}
	defer pg.Stop()
	if err := write(ctx, "TimescaleDB", pg, week); err != nil {
		return err
	}

	prom, err := prometheus.New(model(prometheus.Kind, map[string]any{
		"url": "http://localhost:19090", "auth": "none", "write_enabled": true,
	}), deps)
	if err != nil {
		return err
	}
	// A second seed overlaps the first, which Prometheus refuses as out of
	// order; its earlier samples are still there to draw.
	if err := write(ctx, "Prometheus", prom.(tsdb.Sink), series(now.Add(-50*time.Minute), now, 30*time.Second)); err != nil {
		fmt.Printf("Prometheus kept its earlier seed: %v\n", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
