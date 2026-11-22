package engine

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// maxPayloadBytes bounds stored payload blobs; larger payloads are replaced
// with a truncation marker so stored JSON stays parseable.
const maxPayloadBytes = 64 * 1024

// historyBuffer is the queue depth of the async writer; writes beyond it are
// dropped with a warning rather than blocking the engine.
const historyBuffer = 1024

// History is the engine's persistence sink for runs and run events. All
// methods must be non-blocking; implementations write asynchronously.
type History interface {
	// RunStarted persists a new running row, replacing the flow's previous
	// run and its events (bounded run history).
	RunStarted(run *models.Run)
	// Event appends one run event row.
	Event(ev *models.RunEvent)
	// RunFinished records a run's terminal state.
	RunFinished(runID uuid.UUID, status string, endedAt time.Time, errMsg string, hadFailures bool)
	// Close flushes queued writes and stops the writer.
	Close()
}

// DBHistory writes run history to the database from a single writer
// goroutine fed by a buffered channel.
type DBHistory struct {
	log    *zap.Logger
	ch     chan func()
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

// NewDBHistory creates the sink and starts its writer goroutine.
func NewDBHistory(log *zap.Logger) *DBHistory {
	h := &DBHistory{
		log: log,
		ch:  make(chan func(), historyBuffer),
	}
	h.wg.Add(1)
	go h.writer()
	return h
}

// writer drains queued write operations until the channel closes.
func (h *DBHistory) writer() {
	defer h.wg.Done()
	for op := range h.ch {
		op()
	}
}

// enqueue queues a write, dropping it with a warning when the buffer is full
// or the sink is closed.
func (h *DBHistory) enqueue(op func()) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	select {
	case h.ch <- op:
	default:
		h.log.Warn("run history buffer full; dropping write")
	}
}

// enqueueTerminal queues a write that must land, waiting for room rather than
// dropping it: a run whose finish is dropped is reported as running forever.
// The wait is bounded so a writer that has stopped draining cannot hold the
// engine.
func (h *DBHistory) enqueueTerminal(op func()) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	select {
	case h.ch <- op:
	case <-time.After(5 * time.Second):
		h.log.Warn("run history buffer stayed full; dropping a run's terminal write")
	}
}

// RunStarted inserts a run and prunes completed history beyond the retention limit.
func (h *DBHistory) RunStarted(run *models.Run) {
	run.TriggerPayload = truncatePayload(run.TriggerPayload)
	h.enqueue(func() {
		if err := models.ReplaceLastRun(run); err != nil {
			h.log.Error("failed to persist run start", zap.Error(err))
		}
	})
}

// Event appends one run event row.
func (h *DBHistory) Event(ev *models.RunEvent) {
	ev.Payload = truncatePayload(ev.Payload)
	h.enqueue(func() {
		if err := models.CreateRunEvent(ev); err != nil {
			h.log.Error("failed to persist run event", zap.Error(err))
		}
	})
}

// RunFinished records the run's terminal state.
func (h *DBHistory) RunFinished(runID uuid.UUID, status string, endedAt time.Time, errMsg string, hadFailures bool) {
	h.enqueueTerminal(func() {
		if err := models.FinishRun(runID, status, endedAt, errMsg, hadFailures); err != nil {
			h.log.Error("failed to persist run finish", zap.Error(err))
		}
	})
}

// Close flushes queued writes and stops the writer goroutine.
func (h *DBHistory) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()
	close(h.ch)
	h.wg.Wait()
}

// truncatePayload replaces oversized payload blobs with a small marker
// object so stored values remain valid JSON.
func truncatePayload(s string) string {
	if len(s) <= maxPayloadBytes {
		return s
	}
	return fmt.Sprintf(`{"_truncated":true,"original_bytes":%d}`, len(s))
}

// TruncatedPayload reports whether a stored payload is the marker
// truncatePayload left in place of one too large to keep.
func TruncatedPayload(s string) bool {
	var marker struct {
		Truncated     bool `json:"_truncated"`
		OriginalBytes *int `json:"original_bytes"`
	}
	return json.Unmarshal([]byte(s), &marker) == nil && marker.Truncated && marker.OriginalBytes != nil
}
