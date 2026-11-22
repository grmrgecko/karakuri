// Package tswriter delivers points to writable time-series connections. Each
// connection has one writer shared by history forwards and write nodes. A
// writer batches points, spools them to disk while its destination is
// failing, and drains the spool oldest first, so readings taken during an
// outage, or across a restart during one, still arrive in order.
package tswriter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"go.uber.org/zap"
)

// Batching bounds.
const (
	batchSize    = 1000
	queueSize    = 10000
	writeTimeout = 30 * time.Second
)

// Flush and backoff timing. Variables so tests can shorten them.
var (
	flushInterval = time.Second
	minBackoff    = time.Second
	maxBackoff    = 60 * time.Second
)

// Writer states reported in Status.
const (
	StateIdle     = "idle"
	StateOK       = "ok"
	StateFailing  = "failing"
	StateDisabled = "disabled"
)

// ErrDisabled is returned by Resolve for a connection that is turned off, or
// whose writing is turned off. Points for it are skipped, not buffered.
var ErrDisabled = errors.New("connection or its writing is disabled")

// Resolver finds the running sink for a connection. It returns ErrDisabled
// when the connection should not receive points, and any other error when
// it should but cannot right now.
type Resolver func(id uuid.UUID) (tsdb.Sink, error)

// Status is a writer's state as connection items and MCP report it.
type Status struct {
	State          string     `json:"state" description:"idle, ok, failing, or disabled."`
	LastError      string     `json:"last_error,omitempty" description:"Most recent delivery failure."`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty" description:"When it happened."`
	LastSuccess    *time.Time `json:"last_success,omitempty" description:"When a batch was last delivered."`
	BufferedPoints int64      `json:"buffered_points" description:"Points waiting on disk."`
	BufferedBytes  int64      `json:"buffered_bytes" description:"Disk space they use."`
	Written        int64      `json:"written" description:"Points delivered since start."`
	Discarded      int64      `json:"discarded" description:"Points dropped because the buffer was full or unreadable."`
	Rejected       int64      `json:"rejected" description:"Points the destination refused as malformed."`
	Skipped        int64      `json:"skipped" description:"Points dropped because the connection or its writing is disabled."`
}

// Writer delivers one connection's points.
type Writer struct {
	id      uuid.UUID
	resolve Resolver
	log     *zap.Logger
	label   string

	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	// mu guards everything below. It is never held across a network call,
	// so status reads, enqueues, and closing never wait on a destination.
	mu      sync.Mutex
	closed  bool
	keep    bool
	pending []tsdb.Point
	spool   *spool
	status  Status
	// bufferCap is the spool limit the sink last reported, used while
	// backing off without asking it.
	bufferCap int64
}

// Registry holds every connection's writer.
type Registry struct {
	dir     string
	resolve Resolver
	log     *zap.Logger

	mu      sync.Mutex
	writers map[uuid.UUID]*Writer
	closed  bool
}

// R is the process-wide registry, set at startup.
var R *Registry

// NewRegistry creates a registry spooling under dir.
func NewRegistry(dir string, resolve Resolver, log *zap.Logger) *Registry {
	return &Registry{dir: dir, resolve: resolve, log: log, writers: map[uuid.UUID]*Writer{}}
}

// Resume starts writers for every connection with a spool left from an
// earlier run, so buffered points drain without waiting for new ones.
func (r *Registry) Resume() {
	entries, err := filepath.Glob(filepath.Join(r.dir, "*"))
	if err != nil {
		return
	}
	for _, path := range entries {
		id, err := uuid.Parse(filepath.Base(path))
		if err != nil {
			continue
		}
		if _, err := r.For(id); err != nil {
			r.log.Error("could not resume history buffer", zap.String("connection", id.String()), zap.Error(err))
		}
	}
}

// For returns the connection's writer, starting it on first use.
func (r *Registry) For(id uuid.UUID) (*Writer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("history writers are stopped")
	}
	if w, ok := r.writers[id]; ok {
		return w, nil
	}
	sp, err := openSpool(filepath.Join(r.dir, id.String()))
	if err != nil {
		return nil, fmt.Errorf("open history buffer: %w", err)
	}
	w := &Writer{
		id:        id,
		resolve:   r.resolve,
		log:       r.log.With(zap.String("connection", id.String())),
		label:     id.String(),
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		spool:     sp,
		bufferCap: tsdb.MiB(0),
		status:    Status{State: StateIdle, BufferedPoints: sp.points, BufferedBytes: sp.bytes},
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.publishGauges()
	r.writers[id] = w
	go w.run()
	return w, nil
}

// Status reports a connection's writer, or nil when it has none.
func (r *Registry) Status(id uuid.UUID) *Status {
	r.mu.Lock()
	w, ok := r.writers[id]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	s := w.Status()
	return &s
}

// Remove stops a connection's writer and deletes its buffer. Called when the
// connection is deleted.
func (r *Registry) Remove(id uuid.UUID) {
	r.mu.Lock()
	w, ok := r.writers[id]
	delete(r.writers, id)
	r.mu.Unlock()
	if ok {
		w.close(false)
		w.mu.Lock()
		_ = w.spool.remove()
		w.mu.Unlock()
		return
	}
	sp, err := openSpool(filepath.Join(r.dir, id.String()))
	if err == nil {
		_ = sp.remove()
	}
}

// Close stops every writer, moving undelivered points to their spools.
func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	writers := make([]*Writer, 0, len(r.writers))
	for _, w := range r.writers {
		writers = append(writers, w)
	}
	r.mu.Unlock()
	for _, w := range writers {
		w.close(true)
	}
}

// Enqueue hands points to the writer without blocking. Points beyond the
// memory queue go to the spool, together with everything still queued and in
// order, so a slow destination never stalls the sources and a burst is never
// delivered out of order.
func (w *Writer) Enqueue(points []tsdb.Point) {
	if len(points) == 0 {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.pending = append(w.pending, points...)
	full := len(w.pending) >= batchSize
	if len(w.pending) > queueSize {
		w.spoolLocked(w.pending)
		w.pending = nil
		w.trimLocked(w.bufferCap)
	}
	w.mu.Unlock()
	if full {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

// Deliver writes points now and returns the destination's answer, for write
// nodes that wait for delivery. Nothing is buffered on failure, so the
// node's retry policy decides what happens next.
func (w *Writer) Deliver(ctx context.Context, points []tsdb.Point) error {
	sink, err := w.resolve(w.id)
	if errors.Is(err, ErrDisabled) {
		return tsdb.Unsupportedf("connection or its writing is disabled")
	}
	if err != nil {
		return tsdb.Unavailable(err)
	}
	points = filterFor(sink.SinkInfo(), points)
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := sink.Write(ctx, points); err != nil {
		e := tsdb.Classify(err)
		w.mu.Lock()
		w.recordError(e)
		w.mu.Unlock()
		return e
	}
	w.mu.Lock()
	w.recordSuccess(len(points))
	w.mu.Unlock()
	return nil
}

// Status returns a copy of the writer's state.
func (w *Writer) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status
	s.BufferedPoints = w.spool.points
	s.BufferedBytes = w.spool.bytes
	return s
}

// run delivers queued and buffered points every flush interval, or sooner
// when a batch fills, until close.
func (w *Writer) run() {
	defer close(w.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	backoff := time.Duration(0)
	var retryAt time.Time
	for {
		select {
		case <-w.stop:
			w.mu.Lock()
			if w.keep {
				w.spoolLocked(w.pending)
			}
			w.pending = nil
			w.mu.Unlock()
			return
		case <-w.wake:
		case <-ticker.C:
		}
		if time.Now().Before(retryAt) {
			// While the destination is failing, queued points go to disk
			// behind the buffered ones, so a crash during an outage loses
			// at most one flush interval.
			w.mu.Lock()
			w.spoolLocked(w.pending)
			w.pending = nil
			w.trimLocked(w.bufferCap)
			w.mu.Unlock()
			continue
		}
		if w.deliver() {
			backoff = 0
			retryAt = time.Time{}
			continue
		}
		if backoff == 0 {
			backoff = minBackoff
		} else {
			backoff = min(backoff*2, maxBackoff)
		}
		retryAt = time.Now().Add(backoff)
	}
}

// deliver sends the spool, oldest segment first, then the queued points. It
// returns false when delivery failed for a reason that may clear; what was
// not delivered is then in the spool, ahead of anything newer.
func (w *Writer) deliver() bool {
	sink, err := w.resolve(w.id)
	if err != nil || !sink.SinkInfo().Writable {
		w.mu.Lock()
		defer w.mu.Unlock()
		if err == nil || errors.Is(err, ErrDisabled) {
			// A connection turned off receives nothing and buffers nothing
			// new; what it buffered before waits for it to come back.
			n := len(w.pending)
			w.pending = nil
			w.status.State = StateDisabled
			w.status.Skipped += int64(n)
			metrics.HistoryPointsTotal.WithLabelValues(w.label, "skipped").Add(float64(n))
			return true
		}
		w.spoolLocked(w.pending)
		w.pending = nil
		w.trimLocked(w.bufferCap)
		if w.spool.empty() {
			return true
		}
		w.recordError(tsdb.Unavailable(err))
		return false
	}
	info := sink.SinkInfo()

	w.mu.Lock()
	if info.BufferBytes > 0 {
		w.bufferCap = info.BufferBytes
	}
	w.trimLocked(w.bufferCap)
	w.mu.Unlock()

	for {
		select {
		case <-w.stop:
			return true
		default:
		}
		w.mu.Lock()
		if w.spool.empty() {
			w.mu.Unlock()
			break
		}
		seq := w.spool.segments[0].seq
		points, err := w.spool.oldest()
		if err != nil {
			n := w.spool.dropOldest()
			w.status.Discarded += n
			metrics.HistoryPointsTotal.WithLabelValues(w.label, "discarded").Add(float64(n))
			w.log.Error("discarded an unreadable history buffer segment", zap.Error(err))
			w.publishGauges()
			w.mu.Unlock()
			continue
		}
		w.mu.Unlock()
		if !w.write(sink, info, points) {
			return false
		}
		w.mu.Lock()
		w.spool.dropSeq(seq)
		w.publishGauges()
		w.mu.Unlock()
	}

	w.mu.Lock()
	batch := w.pending
	w.pending = nil
	w.mu.Unlock()
	for start := 0; start < len(batch); start += batchSize {
		chunk := batch[start:min(start+batchSize, len(batch))]
		if !w.write(sink, info, chunk) {
			// These points are older than anything Enqueue spooled while
			// they were in flight, so they go back in front of it.
			w.mu.Lock()
			if err := w.spool.prepend(batch[start:]); err != nil {
				n := len(batch) - start
				w.status.Discarded += int64(n)
				metrics.HistoryPointsTotal.WithLabelValues(w.label, "discarded").Add(float64(n))
				w.log.Error("could not buffer history points", zap.Int("points", n), zap.Error(err))
			}
			w.trimLocked(w.bufferCap)
			w.publishGauges()
			w.mu.Unlock()
			return false
		}
	}
	return true
}

// write sends one batch without holding the lock. A rejected batch counts as
// delivered so it is not retried; the result is false only for failures
// that may clear, including a close interrupting the write.
func (w *Writer) write(sink tsdb.Sink, info tsdb.SinkInfo, points []tsdb.Point) bool {
	points = filterFor(info, points)
	if len(points) == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(w.ctx, writeTimeout)
	defer cancel()
	err := sink.Write(ctx, points)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		w.recordSuccess(len(points))
		return true
	}
	e := tsdb.Classify(err)
	if e.Retryable() || w.ctx.Err() != nil {
		w.recordError(e)
		return false
	}
	w.status.Rejected += int64(len(points))
	metrics.HistoryPointsTotal.WithLabelValues(w.label, "rejected").Add(float64(len(points)))
	w.recordError(e)
	w.log.Error("history destination rejected a batch", zap.Int("points", len(points)), zap.Error(e))
	return true
}

// spoolLocked appends points to the spool, counting them as discarded if the
// disk write fails.
func (w *Writer) spoolLocked(points []tsdb.Point) {
	if len(points) == 0 {
		return
	}
	if err := w.spool.append(points); err != nil {
		w.status.Discarded += int64(len(points))
		metrics.HistoryPointsTotal.WithLabelValues(w.label, "discarded").Add(float64(len(points)))
		w.log.Error("could not buffer history points", zap.Int("points", len(points)), zap.Error(err))
	}
	w.publishGauges()
}

// trimLocked discards the oldest buffered points beyond max bytes.
func (w *Writer) trimLocked(max int64) {
	if n := w.spool.trim(max); n > 0 {
		w.status.Discarded += n
		metrics.HistoryPointsTotal.WithLabelValues(w.label, "discarded").Add(float64(n))
		w.log.Error("history buffer is full; discarded the oldest points", zap.Int64("points", n))
	}
}

func (w *Writer) recordSuccess(n int) {
	now := time.Now()
	w.status.State = StateOK
	w.status.LastSuccess = &now
	w.status.Written += int64(n)
	metrics.HistoryPointsTotal.WithLabelValues(w.label, "written").Add(float64(n))
}

func (w *Writer) recordError(e *tsdb.Error) {
	now := time.Now()
	if e.Retryable() {
		w.status.State = StateFailing
	}
	w.status.LastError = e.Error()
	w.status.LastErrorAt = &now
}

func (w *Writer) publishGauges() {
	metrics.HistoryBufferedPoints.WithLabelValues(w.label).Set(float64(w.spool.points))
	metrics.HistoryBufferedBytes.WithLabelValues(w.label).Set(float64(w.spool.bytes))
}

// close stops the writer, interrupting a write in flight. With keep set,
// queued points are spooled; a write the close interrupted is spooled too.
func (w *Writer) close(keep bool) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.keep = keep
	w.mu.Unlock()
	w.cancel()
	close(w.stop)
	<-w.done
	metrics.HistoryBufferedPoints.DeleteLabelValues(w.label)
	metrics.HistoryBufferedBytes.DeleteLabelValues(w.label)
}

// filterFor drops points a numeric-only sink cannot store.
func filterFor(info tsdb.SinkInfo, points []tsdb.Point) []tsdb.Point {
	if !info.NumericOnly {
		return points
	}
	out := points[:0:0]
	for _, p := range points {
		if p.Value != nil {
			out = append(out, p)
		}
	}
	return out
}
