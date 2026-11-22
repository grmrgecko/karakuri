package energy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Recorder timing.
const (
	flushEvery = 30 * time.Second
	purgeAfter = 30 * 24 * time.Hour
	purgeEvery = 24 * time.Hour
	eventQueue = 4096
)

// R is the running recorder, set at startup.
var R *Recorder

// live is one source being recorded.
type live struct {
	model *models.EnergySource
	cfg   MeterConfig
	st    MeterState
	dirty bool
	// history says missed hours can be read back, so usage across a
	// silence is filled from history later instead of carried.
	history bool
}

// hourKey names one source's hour.
type hourKey struct {
	source uuid.UUID
	hour   time.Time
}

// hourTotal is one source-hour held in memory: its total so far, read from
// the database on first touch, and whether it needs writing.
type hourTotal struct {
	kwh     float64
	quality string
	dirty   bool
}

// change is one reading on its way to the worker.
type change struct {
	ref   string
	value any
	at    time.Time
	attrs map[string]any
}

// Recorder turns change streams into hourly usage.
type Recorder struct {
	log *zap.Logger
	now func() time.Time

	events chan change
	reload chan struct{}
	flush  chan chan struct{}
	stop   chan struct{}
	done   chan struct{}

	unwatch []func()

	// Worker-owned state.
	sources map[uuid.UUID]*live
	byRef   map[string][]*live
	hours   map[hourKey]*hourTotal
	rates   *rateTracker
	gaps    []gapWindow

	mu      sync.Mutex
	started bool
}

// NewRecorder builds a recorder.
func NewRecorder(log *zap.Logger) *Recorder {
	if log == nil {
		log = zap.NewNop()
	}
	return &Recorder{
		log:     log,
		now:     time.Now,
		events:  make(chan change, eventQueue),
		reload:  make(chan struct{}, 1),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		sources: map[uuid.UUID]*live{},
		byRef:   map[string][]*live{},
		hours:   map[hourKey]*hourTotal{},
		rates:   newRateTracker(),
	}
}

// controlRef, deviceRef, and haRef name the change streams sources read.
func controlRef(id string) string      { return "control:" + id }
func deviceRef(id string) string       { return "device:" + id }
func haRef(conn, entity string) string { return "ha:" + conn + ":" + entity }

// sourceRef is the change stream reference a source reads.
func sourceRef(s *models.EnergySource) string {
	switch s.SourceKind {
	case models.EnergyKindControl:
		return controlRef(strings.ToLower(s.SourceID))
	case models.EnergyKindDevice:
		return deviceRef(strings.ToLower(s.SourceID))
	}
	return haRef(strings.ToLower(s.SourceID), s.EntityID)
}

// Start subscribes to the change streams and starts the worker.
func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.started = true
	if controls.S != nil {
		r.unwatch = append(r.unwatch, controls.S.Watch(func(c controls.Change) {
			r.push(change{ref: controlRef(c.Control.ID.String()), value: c.Control.Value, at: c.At})
		}))
	}
	if mqttrelay.D != nil {
		r.unwatch = append(r.unwatch, mqttrelay.D.Watch(func(c mqttrelay.DeviceChange) {
			r.push(change{ref: deviceRef(c.DeviceID.String()), value: c.State, attrs: c.Values, at: r.now()})
		}))
	}
	sub := mq.B.Subscribe("ha:*:state:*", func(m *mq.Message) {
		if m.Type != "ha_state" {
			return
		}
		parts := strings.SplitN(m.Topic, ":", 4)
		if len(parts) < 4 {
			return
		}
		var payload struct {
			NewState *struct {
				EntityID    string         `json:"entity_id"`
				State       string         `json:"state"`
				Attributes  map[string]any `json:"attributes"`
				LastUpdated string         `json:"last_updated"`
			} `json:"new_state"`
		}
		if json.Unmarshal(m.Payload, &payload) != nil || payload.NewState == nil {
			return
		}
		st := payload.NewState
		at, err := time.Parse(time.RFC3339Nano, st.LastUpdated)
		if err != nil {
			at = r.now()
		}
		r.push(change{ref: haRef(strings.ToLower(parts[1]), st.EntityID), value: st.State, attrs: st.Attributes, at: at})
	})
	r.unwatch = append(r.unwatch, func() { mq.B.Unsubscribe(sub) })
	go r.run()
	r.Reload()
}

// Stop flushes and stops the worker.
func (r *Recorder) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	unwatch := r.unwatch
	r.unwatch = nil
	r.mu.Unlock()
	for _, u := range unwatch {
		u()
	}
	close(r.stop)
	<-r.done
}

// Reload rereads the sources and tariffs. Safe from any goroutine.
func (r *Recorder) Reload() {
	select {
	case r.reload <- struct{}{}:
	default:
	}
}

// Flush writes pending hours now and waits for it, for reads that must see
// the latest usage and for tests.
func (r *Recorder) Flush() {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		return
	}
	done := make(chan struct{})
	select {
	case r.flush <- done:
		<-done
	case <-r.done:
	}
}

func (r *Recorder) push(c change) {
	select {
	case r.events <- c:
	default:
		r.log.Warn("energy recorder queue full; reading dropped", zap.String("source", c.ref))
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	lastPurge := time.Time{}
	for {
		select {
		case <-r.stop:
			r.drain()
			r.write()
			return
		case <-r.reload:
			r.write()
			r.load()
		case c := <-r.events:
			r.pendingReload()
			r.handle(c)
		case done := <-r.flush:
			// A flush is a promise that readings already pushed are in the
			// database, so the queue is drained first.
			r.drain()
			r.write()
			close(done)
		case now := <-tick.C:
			r.write()
			r.fillDue(now)
			if now.Sub(lastPurge) >= purgeEvery {
				lastPurge = now
				if n, err := models.PurgeDeletedEnergySources(now.Add(-purgeAfter)); err != nil {
					r.log.Error("failed to purge deleted energy sources", zap.Error(err))
				} else if n > 0 {
					r.log.Info("purged deleted energy sources", zap.Int("count", n))
				}
			}
		}
	}
}

// pendingReload applies a reload asked for before the readings about to be
// handled, so a source edited and then fed reads with its new settings.
func (r *Recorder) pendingReload() {
	select {
	case <-r.reload:
		r.write()
		r.load()
	default:
	}
}

// drain handles every queued reading.
func (r *Recorder) drain() {
	r.pendingReload()
	for {
		select {
		case c := <-r.events:
			r.handle(c)
		default:
			return
		}
	}
}

// load rereads the sources, keeping the meter state of sources that stay.
func (r *Recorder) load() {
	list, err := models.ListEnergySources()
	if err != nil {
		r.log.Error("failed to load energy sources", zap.Error(err))
		return
	}
	next := map[uuid.UUID]*live{}
	byRef := map[string][]*live{}
	for _, m := range list {
		l := &live{model: m, cfg: ConfigOf(m), history: hasHistory(m)}
		if old, ok := r.sources[m.ID]; ok && sameReading(old.model, m) {
			l.st = old.st
		} else {
			l.st = MeterState{Last: m.LastValue, LastAt: m.LastAt, State: m.State, Note: m.StateNote}
			if l.st.Last != nil {
				// The stored value is in the source's unit.
				if f, err := UnitFactor(m.Measurement, m.Unit); err == nil {
					v := *l.st.Last * f
					l.st.Last = &v
				}
			}
			if l.st.State == "" {
				l.st.State = StateWaiting
			}
		}
		next[m.ID] = l
		ref := sourceRef(m)
		byRef[ref] = append(byRef[ref], l)
	}
	r.sources, r.byRef = next, byRef
	if err := r.rates.load(); err != nil {
		r.log.Error("failed to load energy tariffs", zap.Error(err))
	}
	r.log.Debug("energy sources loaded", zap.Int("count", len(next)))
}

// sameReading reports whether a source's edit leaves what it reads alone,
// so its meter state carries over.
func sameReading(a, b *models.EnergySource) bool {
	return a.SourceKind == b.SourceKind && strings.EqualFold(a.SourceID, b.SourceID) && a.EntityID == b.EntityID &&
		a.Attribute == b.Attribute && a.Measurement == b.Measurement && strings.EqualFold(a.Unit, b.Unit)
}

// handle applies one change to the sources reading it.
func (r *Recorder) handle(c change) {
	if r.rates.watches(c.ref) {
		if v, ok := toNumber(c.value); ok {
			r.rates.sample(v, c.at)
		}
	}
	targets := r.byRef[c.ref]
	if len(targets) == 0 {
		return
	}
	loc := Location()
	now := r.now()
	for _, l := range targets {
		raw, present := pick(l.model, c)
		if !present {
			continue
		}
		v, ok := toNumber(raw)
		if !ok {
			l.st = Unavailable(l.cfg, l.st, fmt.Sprintf("reported %v", raw))
			r.mark(l.model.ID, HourStart(c.at, loc), 0, models.EnergyQualityPartial)
			l.dirty = true
			continue
		}
		var cs []Contribution
		before := l.st.LastAt
		l.st, cs = Apply(l.cfg, l.st, v, c.at, now, loc, false)
		for _, ct := range cs {
			gap := ct.Quality == models.EnergyQualityCarried ||
				(ct.Quality == models.EnergyQualityPartial && l.cfg.Measurement == models.EnergyMeasurePower)
			if gap && l.history && before != nil {
				// The history has the hours; fill them once they close
				// rather than piling the silence into this hour.
				r.gaps = append(r.gaps, gapWindow{source: l.model.ID, from: *before, to: c.at})
				l.st.Note = "usage across the silence is filled from history"
				// The silence starts and ends inside an hour that also
				// holds live usage. A fill leaves live hours alone, so
				// both are marked partial for it to replace whole.
				r.mark(l.model.ID, HourStart(*before, loc), 0, models.EnergyQualityPartial)
				r.mark(l.model.ID, HourStart(c.at, loc), 0, models.EnergyQualityPartial)
				continue
			}
			r.mark(l.model.ID, ct.Hour, ct.KWh, ct.Quality)
		}
		l.dirty = true
	}
}

// pick reads a source's value from a change; false when the change does not
// carry the attribute the source reads.
func pick(s *models.EnergySource, c change) (any, bool) {
	switch s.SourceKind {
	case models.EnergyKindDevice:
		attr := s.Attribute
		if attr == "" {
			attr = "value"
		}
		// Only a message that carried the attribute is a reading; the merged
		// state repeats old values on every message.
		if _, carried := c.attrs[attr]; !carried {
			return nil, false
		}
		state, _ := c.value.(map[string]any)
		v, ok := state[attr]
		return v, ok
	case models.EnergyKindHA:
		if s.Attribute == "" {
			return c.value, true
		}
		v, ok := c.attrs[s.Attribute]
		return v, ok
	}
	if s.Attribute == "" {
		return c.value, true
	}
	m, ok := c.value.(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := m[s.Attribute]
	return v, ok
}

// toNumber reads a reading as a number.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// mark adds usage to one source-hour, reading its stored total first.
func (r *Recorder) mark(source uuid.UUID, hour time.Time, kwh float64, quality string) {
	k := hourKey{source, hour.UTC()}
	h, ok := r.hours[k]
	if !ok {
		h = &hourTotal{}
		var row models.EnergyHour
		if err := db.DB.Where("source_id = ? AND hour = ?", source, k.hour).First(&row).Error; err == nil {
			h.kwh, h.quality = row.KWh, row.Quality
		}
		r.hours[k] = h
	}
	h.kwh += kwh
	h.quality = worse(h.quality, quality)
	h.dirty = true
}

// write stores dirty hours, meter states, and the hour's prices.
func (r *Recorder) write() {
	loc := Location()
	now := r.now()
	var rows []models.EnergyHour
	earliest := now
	for k, h := range r.hours {
		if h.dirty {
			if k.hour.Before(earliest) {
				earliest = k.hour
			}
			q := h.quality
			if q == "" {
				q = models.EnergyQualityLive
			}
			rows = append(rows, models.EnergyHour{SourceID: k.source, Hour: k.hour, LocalDate: LocalDate(k.hour, loc), KWh: h.kwh, Quality: q})
		}
	}
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.UpsertEnergyHours(tx, rows); err != nil {
			return err
		}
		for _, l := range r.sources {
			if !l.dirty {
				continue
			}
			updates := map[string]any{"state": l.st.State, "state_note": l.st.Note, "last_value": nil, "last_at": l.st.LastAt}
			if l.st.Last != nil {
				if f, err := UnitFactor(l.model.Measurement, l.model.Unit); err == nil && f != 0 {
					updates["last_value"] = *l.st.Last / f
				}
			}
			// The row may have been edited to read something else since it
			// was loaded, which clears its last reading; this state belongs
			// to the old reading and must not be written back over that.
			m := l.model
			q := tx.Model(&models.EnergySource{}).Where("id = ? AND source_kind = ? AND source_id = ? AND entity_id = ? AND attribute = ? AND measurement = ? AND unit = ?",
				m.ID, m.SourceKind, m.SourceID, m.EntityID, m.Attribute, m.Measurement, m.Unit)
			if err := q.Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.log.Error("failed to write energy hours", zap.Error(err))
		return
	}
	for _, h := range r.hours {
		h.dirty = false
	}
	for _, l := range r.sources {
		l.dirty = false
	}
	// Hours more than two hours old will not be touched live again.
	cutoff := now.Add(-2 * time.Hour)
	for k := range r.hours {
		if k.hour.Before(cutoff) {
			delete(r.hours, k)
		}
	}
	if err := r.rates.write(now, earliest, loc); err != nil {
		r.log.Error("failed to write energy prices", zap.Error(err))
	}
}
