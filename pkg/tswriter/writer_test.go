package tswriter

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeSink records delivered points and fails with a configured error. It
// stands in for the database at the Sink boundary.
type fakeSink struct {
	mu     sync.Mutex
	got    []tsdb.Point
	fail   error
	buffer int64
}

func (f *fakeSink) Write(_ context.Context, points []tsdb.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, points...)
	return nil
}

func (f *fakeSink) SinkInfo() tsdb.SinkInfo {
	return tsdb.SinkInfo{Writable: true, BufferBytes: f.buffer}
}

func (f *fakeSink) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func (f *fakeSink) times() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.got))
	for i, p := range f.got {
		out[i] = p.Time
	}
	return out
}

// fastTiming shortens the flush tick and backoff so outage tests finish in
// well under a second instead of waiting out real backoff.
func fastTiming(t *testing.T) {
	t.Helper()
	flush, lo, hi := flushInterval, minBackoff, maxBackoff
	flushInterval, minBackoff, maxBackoff = 10*time.Millisecond, 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { flushInterval, minBackoff, maxBackoff = flush, lo, hi })
}

func points(from, n int) []tsdb.Point {
	out := make([]tsdb.Point, n)
	for i := range out {
		out[i] = tsdb.Point{Time: int64(from + i), Metric: "m", Value: tsdb.Float(float64(from + i))}
	}
	return out
}

// TestOutageSurvivesRestart pins spec 027 user story 3: points produced
// while the destination is down are spooled, survive the registry closing
// and reopening on the same directory, and arrive oldest first once the
// destination answers again.
func TestOutageSurvivesRestart(t *testing.T) {
	fastTiming(t)
	dir := t.TempDir()
	id := uuid.New()
	sink := &fakeSink{fail: tsdb.Unavailablef("connection refused"), buffer: 64 << 20}
	resolve := func(uuid.UUID) (tsdb.Sink, error) { return sink, nil }

	reg := NewRegistry(dir, resolve, zap.NewNop())
	w, err := reg.For(id)
	require.NoError(t, err)
	w.Enqueue(points(1, 10))
	require.Eventually(t, func() bool { return w.Status().BufferedPoints == 10 }, 5*time.Second, 20*time.Millisecond,
		"a failed delivery must leave the points on disk")
	w.Enqueue(points(11, 10))
	reg.Close()

	sink.setFail(nil)
	reg = NewRegistry(dir, resolve, zap.NewNop())
	reg.Resume()
	t.Cleanup(reg.Close)
	require.Eventually(t, func() bool { return len(sink.times()) == 20 }, 10*time.Second, 20*time.Millisecond,
		"every buffered point must be delivered after the restart")
	want := make([]int64, 20)
	for i := range want {
		want[i] = int64(i + 1)
	}
	assert.Equal(t, want, sink.times(), "buffered points must arrive in the order they were produced")
	assert.Equal(t, int64(0), reg.Status(id).BufferedPoints)
}

// TestBufferCapAndRejection pins the two ways points are dropped: a full
// buffer discards the oldest segments and counts them, and a batch the
// destination rejects as malformed is counted once and not retried, so
// later batches still flow.
func TestBufferCapAndRejection(t *testing.T) {
	fastTiming(t)
	dir := t.TempDir()
	sink := &fakeSink{fail: tsdb.Unavailablef("down"), buffer: 250}
	reg := NewRegistry(dir, func(uuid.UUID) (tsdb.Sink, error) { return sink, nil }, zap.NewNop())
	t.Cleanup(reg.Close)
	w, err := reg.For(uuid.New())
	require.NoError(t, err)

	// A point line such as {"t":1,"m":"m","v":1} is about 22 bytes, so a
	// 5-point segment is about 110 bytes and a 250-byte buffer holds two
	// segments and discards the first of three.
	for i := 0; i < 3; i++ {
		w.Enqueue(points(i*5+1, 5))
		require.Eventually(t, func() bool { return w.Status().BufferedPoints+w.Status().Discarded == int64(i*5+5) },
			5*time.Second, 10*time.Millisecond, fmt.Sprintf("batch %d must be buffered", i))
	}
	require.Eventually(t, func() bool { return w.Status().Discarded > 0 }, 5*time.Second, 10*time.Millisecond)
	st := w.Status()
	assert.Equal(t, int64(15), st.BufferedPoints+st.Discarded, "every point is either buffered or counted as discarded")

	sink.setFail(nil)
	require.Eventually(t, func() bool { return w.Status().BufferedPoints == 0 }, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, int64(15), sink.times()[len(sink.times())-1], "the newest points survive a full buffer")

	sink.setFail(tsdb.Rejectedf("field type conflict"))
	w.Enqueue(points(100, 3))
	require.Eventually(t, func() bool { return w.Status().Rejected == 3 }, 5*time.Second, 10*time.Millisecond)
	sink.setFail(nil)
	w.Enqueue(points(200, 2))
	require.Eventually(t, func() bool { return w.Status().Written == st.BufferedPoints+2 }, 5*time.Second, 10*time.Millisecond,
		"a rejected batch must not block later ones")
	assert.Equal(t, int64(0), w.Status().BufferedPoints, "a rejected batch is not buffered for retry")
}

// TestBurstKeepsOrder pins that a burst larger than the memory queue, sent
// while the destination is failing, still arrives oldest first. Prometheus
// refuses a sample older than one it already holds for a series, so a
// writer that delivered the overflow before the queued points would lose
// the older ones for good.
func TestBurstKeepsOrder(t *testing.T) {
	fastTiming(t)
	sink := &fakeSink{fail: tsdb.Unavailablef("down"), buffer: 64 << 20}
	reg := NewRegistry(t.TempDir(), func(uuid.UUID) (tsdb.Sink, error) { return sink, nil }, zap.NewNop())
	t.Cleanup(reg.Close)
	w, err := reg.For(uuid.New())
	require.NoError(t, err)

	// 6,000 + 6,000 passes the 10,000-point queue on the second call.
	w.Enqueue(points(1, 6000))
	w.Enqueue(points(6001, 6000))
	w.Enqueue(points(12001, 100))
	time.Sleep(50 * time.Millisecond)
	sink.setFail(nil)
	require.Eventually(t, func() bool { return len(sink.times()) == 12100 }, 10*time.Second, 20*time.Millisecond,
		"every point of the burst must be delivered")
	got := sink.times()
	for i := 1; i < len(got); i++ {
		require.Less(t, got[i-1], got[i], "point %d arrived after a newer one", i)
	}
}
