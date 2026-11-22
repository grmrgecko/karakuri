package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Limits on what one entry carries and how fast entries move.
const (
	queueSize      = 4096
	batchSize      = 100
	batchInterval  = 250 * time.Millisecond
	foldWindow     = time.Minute
	maxMessage     = 500
	maxDetailBytes = 8 << 10
)

// Subject is what an entry is about.
type Subject struct {
	Kind string
	ID   string
	Name string
}

// Entry is an event to record.
type Entry struct {
	Category string
	Level    string
	Subject  Subject
	Message  string
	Details  map[string]any
	Actor    Actor
	// At defaults to now.
	At time.Time
}

// Publisher delivers recorded entries to live viewers.
type Publisher interface {
	Publish(topic, msgType string, payload interface{})
}

// R is the server's recorder, set by serve. Record is a no-op without one.
var R *Recorder

// Record queues an entry on the server's recorder. It never blocks: a full
// queue drops the entry, since the action it describes must not wait on the
// log.
func Record(e Entry) {
	if R != nil {
		R.Record(e)
	}
}

// Recorder writes entries in the background, folding repeats.
type Recorder struct {
	pub Publisher
	log *zap.Logger
	now func() time.Time

	queue   chan models.Event
	dropped atomic.Int64
	stop    chan struct{}
	done    chan struct{}

	// folds maps a subject to its latest row, which an identical entry for
	// the same subject folds into. Only the writer goroutine touches it.
	folds map[string]fold

	mu       sync.Mutex
	watchers map[int]func(models.Event, bool)
	nextID   int
}

type fold struct {
	key   string
	id    uint
	at    time.Time
	count int
}

// NewRecorder returns a recorder publishing to pub, which may be nil.
func NewRecorder(pub Publisher, log *zap.Logger) *Recorder {
	if log == nil {
		log = zap.NewNop()
	}
	return &Recorder{
		pub: pub, log: log, now: time.Now,
		queue: make(chan models.Event, queueSize),
		stop:  make(chan struct{}), done: make(chan struct{}),
		folds:    map[string]fold{},
		watchers: map[int]func(models.Event, bool){},
	}
}

// Record queues an entry, dropping it when the queue is full.
func (r *Recorder) Record(e Entry) {
	ev := toModel(e, r.now())
	select {
	case r.queue <- ev:
	default:
		r.dropped.Add(1)
	}
}

// toModel fills defaults and applies the size limits.
func toModel(e Entry, now time.Time) models.Event {
	at := e.At
	if at.IsZero() {
		at = now
	}
	at = at.UTC()
	if e.Level == "" {
		e.Level = models.EventInfo
	}
	if !e.Actor.Present() {
		e.Actor = System()
	}
	msg := []rune(e.Message)
	if len(msg) > maxMessage {
		e.Message = string(msg[:maxMessage-1]) + "…"
	}
	details := e.Details
	if details != nil {
		if raw, err := json.Marshal(details); err != nil || len(raw) > maxDetailBytes {
			details = map[string]any{"truncated": true}
		}
	}
	return models.Event{
		At: at, LastAt: at, Count: 1,
		Category: e.Category, Level: e.Level,
		SubjectKind: e.Subject.Kind, SubjectID: e.Subject.ID, SubjectName: e.Subject.Name,
		Message: e.Message, Details: details,
		ActorKind: e.Actor.Kind, ActorID: e.Actor.ID, ActorName: e.Actor.Name,
		Via: e.Actor.Via, DashboardID: e.Actor.DashboardID, RunID: e.Actor.RunID, Address: e.Actor.Address,
	}
}

// Watch registers fn for every written entry. update is true when a repeat
// folded into an existing entry rather than adding one.
func (r *Recorder) Watch(fn func(ev models.Event, update bool)) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextID
	r.nextID++
	r.watchers[id] = fn
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.watchers, id)
	}
}

// Start runs the writer until Stop.
func (r *Recorder) Start() {
	go r.run()
}

// Stop writes what is queued and stops the writer.
func (r *Recorder) Stop() {
	close(r.stop)
	<-r.done
}

func (r *Recorder) run() {
	defer close(r.done)
	tick := time.NewTicker(batchInterval)
	defer tick.Stop()
	report := time.NewTicker(time.Minute)
	defer report.Stop()
	batch := make([]models.Event, 0, batchSize)
	flush := func() {
		if len(batch) > 0 {
			r.write(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case ev := <-r.queue:
			batch = append(batch, ev)
			if len(batch) >= batchSize {
				flush()
			}
		case <-tick.C:
			flush()
		case <-report.C:
			if n := r.dropped.Swap(0); n > 0 {
				r.log.Error("event log queue full; entries dropped", zap.Int64("dropped", n))
			}
		case <-r.stop:
			for {
				select {
				case ev := <-r.queue:
					batch = append(batch, ev)
				default:
					flush()
					return
				}
			}
		}
	}
}

// foldKey identifies entries that fold together: the same thing happening to
// the same subject by the same actor from the same address, so a second
// client's failed sign-ins are never hidden in the first one's count.
// subjectKey groups entries by subject,
// so an entry folds only into its subject's latest one and a change in
// between is never hidden behind a count.
func foldKey(ev models.Event) string {
	return ev.Level + "\x00" + ev.Message + "\x00" + ev.ActorKind + "\x00" + ev.ActorID + "\x00" + ev.Address
}

func subjectKey(ev models.Event) string {
	if ev.SubjectKind == "" {
		return ev.Category + "\x00" + ev.Message
	}
	return ev.Category + "\x00" + ev.SubjectKind + "\x00" + ev.SubjectID
}

// written is one entry after the batch is stored.
type written struct {
	ev     models.Event
	update bool
}

// write stores a batch in one transaction, folding repeats into the row they
// repeat, then delivers the results. A failed write is logged and the batch
// dropped; the actions it describes already happened.
func (r *Recorder) write(batch []models.Event) {
	var out []written
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		out = out[:0]
		for _, ev := range batch {
			key, subject := foldKey(ev), subjectKey(ev)
			if f, ok := r.folds[subject]; ok && f.key == key && ev.At.Sub(f.at) < foldWindow && ev.At.Sub(f.at) >= 0 {
				f.count++
				if err := tx.Model(&models.Event{}).Where("id = ?", f.id).
					Updates(map[string]any{"count": f.count, "last_at": ev.At}).Error; err != nil {
					return err
				}
				r.folds[subject] = f
				ev.ID, ev.At, ev.Count, ev.LastAt = f.id, f.at, f.count, ev.At
				out = append(out, written{ev: ev, update: true})
				continue
			}
			if err := tx.Create(&ev).Error; err != nil {
				return err
			}
			r.folds[subject] = fold{key: key, id: ev.ID, at: ev.At, count: 1}
			out = append(out, written{ev: ev})
		}
		return nil
	})
	if err != nil {
		r.log.Error("writing event log entries", zap.Int("entries", len(batch)), zap.Error(err))
		r.folds = map[string]fold{}
		return
	}
	r.pruneFolds()
	for _, w := range out {
		r.deliver(w.ev, w.update)
	}
}

// pruneFolds forgets fold targets past the window.
func (r *Recorder) pruneFolds() {
	if len(r.folds) < 1024 {
		return
	}
	cutoff := r.now().Add(-foldWindow)
	for k, f := range r.folds {
		if f.at.Before(cutoff) {
			delete(r.folds, k)
		}
	}
}

// Topic names for live delivery.
const (
	TopicAll      = "events"
	TopicSecurity = "events:security"
)

// SubjectTopic is the live topic for one subject's entries.
func SubjectTopic(kind, id string) string { return "events:subject:" + kind + ":" + id }

func (r *Recorder) deliver(ev models.Event, update bool) {
	msgType := "event"
	if update {
		msgType = "event_update"
	}
	if r.pub != nil {
		if ev.Category == models.EventSecurity {
			r.pub.Publish(TopicSecurity, msgType, ev)
		} else {
			r.pub.Publish(TopicAll, msgType, ev)
			if ev.SubjectKind != "" && ev.SubjectID != "" {
				r.pub.Publish(SubjectTopic(ev.SubjectKind, ev.SubjectID), msgType, ev)
			}
		}
	}
	r.mu.Lock()
	fns := make([]func(models.Event, bool), 0, len(r.watchers))
	for _, fn := range r.watchers {
		fns = append(fns, fn)
	}
	r.mu.Unlock()
	for _, fn := range fns {
		fn(ev, update)
	}
}

// WriteNow stores one entry immediately on gdb, for the command line, where
// no server is running to record it.
func WriteNow(gdb *gorm.DB, e Entry) error {
	ev := toModel(e, time.Now())
	return gdb.Create(&ev).Error
}
