package historyfwd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tswriter"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// recordingSink stands in for a Prometheus connection at the Sink boundary:
// numbers only, a 60 second heartbeat, and a record of what it was sent.
type recordingSink struct {
	mu  sync.Mutex
	got []tsdb.Point
}

func (s *recordingSink) Write(_ context.Context, points []tsdb.Point) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, points...)
	return nil
}

func (s *recordingSink) SinkInfo() tsdb.SinkInfo {
	return tsdb.SinkInfo{Writable: true, NumericOnly: true, Heartbeat: time.Minute}
}

func (s *recordingSink) points() []tsdb.Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tsdb.Point{}, s.got...)
}

// TestQuietSeriesReachLateDestination pins that a destination whose
// heartbeat was unknown when the forwards loaded still gets the current
// value of a control that never changes. The connection is not running at
// the load, so nothing is seeded for it, and it used to stay that way after
// the connection came up: the 30 second refresh learned the heartbeat and
// seeded nothing, so a quiet control had no series until its first change or
// until some forward was saved.
func TestQuietSeriesReachLateDestination(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	setpoint, err := controls.S.Create("Setpoint", controls.KindNumber, "", nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = controls.S.Delete(setpoint.ID) })
	dest := uuid.New()
	require.NoError(t, db.DB.Create(&models.HistoryForward{
		Name: "Setpoints", Enabled: true, SourceType: models.ForwardSourceControl,
		SourceIDs:    models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{setpoint.ID}},
		Destinations: models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{dest}},
	}).Error)

	sink := &recordingSink{}
	var mu sync.Mutex
	running := false
	resolve := func(uuid.UUID) (tsdb.Sink, error) {
		mu.Lock()
		defer mu.Unlock()
		if !running {
			return nil, errors.New("connection is not running")
		}
		return sink, nil
	}
	writers := tswriter.NewRegistry(t.TempDir(), resolve, zap.NewNop())
	t.Cleanup(writers.Close)
	f := New(Deps{Log: zap.NewNop(), Sink: resolve, Writer: writers.For})

	// The forwards load while the connection is down.
	f.loadIndex()
	f.refreshDests()
	f.seed()
	f.heartbeat(time.Now())

	mu.Lock()
	running = true
	mu.Unlock()
	f.refreshDests()
	f.heartbeat(time.Now())

	require.Eventually(t, func() bool { return len(sink.points()) > 0 }, 5*time.Second, 20*time.Millisecond,
		"the control's current value must be sent once the destination answers")
	p := sink.points()[0]
	assert.Equal(t, metricControl, p.Metric)
	assert.Equal(t, setpoint.ID.String(), p.Labels["id"])
}
