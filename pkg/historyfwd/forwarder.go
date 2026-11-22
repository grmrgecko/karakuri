// Package historyfwd records control, MQTT device, and Home Assistant values
// in time-series databases. It watches the three sources, matches each
// change against the history forwards that select it, shapes the values for
// each destination, and hands the points to that destination's writer. It
// also re-sends each series' last value at the destination's heartbeat, so
// databases that forget quiet series, such as Prometheus, keep them.
package historyfwd

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tswriter"
	"go.uber.org/zap"
)

const (
	eventQueue    = 4096
	tickInterval  = time.Second
	refreshEvery  = 30 * time.Second
	pruneEvery    = 60 * time.Second
	sourceControl = "control"
	sourceDevice  = "device"
	sourceHA      = "ha"
)

// Deps are the forwarder's collaborators, set by the server at startup.
type Deps struct {
	Log *zap.Logger
	// Sink resolves a destination's sink, for its numeric-only and heartbeat
	// settings. Disabled destinations return an error.
	Sink func(id uuid.UUID) (tsdb.Sink, error)
	// Writer returns a destination's writer.
	Writer func(id uuid.UUID) (*tswriter.Writer, error)
	// HAStates returns a Home Assistant connection's cached states, for
	// seeding heartbeats and pruning entities that are gone.
	HAStates func(ctx context.Context, connID uuid.UUID) ([]homeassistant.Entity, error)
	// ConnectionName names a connection for the connection label.
	ConnectionName func(id uuid.UUID) string
}

// event is one source change waiting for the worker.
type event struct {
	source  string
	control *controls.Change
	device  *mqttrelay.DeviceChange
	haConn  uuid.UUID
	haState *haState
	at      time.Time
}

// haState is the part of a Home Assistant state object the forwarder reads.
type haState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastUpdated string         `json:"last_updated"`
}

// compiled is a forward ready to match.
type compiled struct {
	f       *models.HistoryForward
	ids     map[uuid.UUID]bool
	include []*regexp.Regexp
	exclude []*regexp.Regexp
	metric  string
}

// index holds the enabled forwards by what they select.
type index struct {
	controls    map[uuid.UUID][]*compiled
	allControls []*compiled
	devices     map[uuid.UUID][]*compiled
	allDevices  []*compiled
	ha          map[uuid.UUID][]*compiled
	byID        map[uuid.UUID]*compiled
}

// destInfo caches what each destination wants.
type destInfo struct {
	numericOnly bool
	heartbeat   time.Duration
	ok          bool
}

// beat is one series' last point at one destination.
type beat struct {
	dest    uuid.UUID
	forward uuid.UUID
	source  string
	ref     string
	ident   string
	point   tsdb.Point
	sentAt  time.Time
}

// enumState is the previous value of an enumeration whose options are
// unknown, used to zero its last state-set series.
type enumState struct {
	value string
	at    time.Time
}

// staleAfter bounds how long per-series bookkeeping outlives its last
// update: past the longest minimum interval a forward may set, an entry
// no longer changes any decision.
const staleAfter = 25 * time.Hour

// pending is the latest value of a series held back by a minimum interval.
type pending struct {
	dest     uuid.UUID
	point    tsdb.Point
	interval time.Duration
}

// Forwarder turns source changes into points.
type Forwarder struct {
	deps   Deps
	events chan event
	reload chan struct{}
	stop   chan struct{}
	done   chan struct{}

	unwatch []func()

	// Worker-owned state.
	idx      *index
	dests    map[uuid.UUID]destInfo
	beats    map[string]*beat
	held     map[string]pending
	lastSent map[string]time.Time
	enumPrev map[string]enumState
	// identKey maps a series' identity (destination, forward, source
	// attribute, and state label) to the key its labels currently make, so
	// a rename replaces the old series' heartbeat instead of adding one.
	identKey map[string]string

	mu      sync.Mutex
	started bool
}

// F is the running forwarder, set at startup.
var F *Forwarder

// New creates a forwarder.
func New(deps Deps) *Forwarder {
	return &Forwarder{
		deps:     deps,
		events:   make(chan event, eventQueue),
		reload:   make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		dests:    map[uuid.UUID]destInfo{},
		beats:    map[string]*beat{},
		held:     map[string]pending{},
		lastSent: map[string]time.Time{},
		enumPrev: map[string]enumState{},
		identKey: map[string]string{},
	}
}

// Start subscribes to the sources and starts the worker.
func (f *Forwarder) Start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started {
		return
	}
	f.started = true
	f.unwatch = append(f.unwatch, controls.S.Watch(func(c controls.Change) {
		f.push(event{source: sourceControl, control: &c, at: c.At})
	}))
	if mqttrelay.D != nil {
		f.unwatch = append(f.unwatch, mqttrelay.D.Watch(func(c mqttrelay.DeviceChange) {
			f.push(event{source: sourceDevice, device: &c, at: time.Now()})
		}))
		f.unwatch = append(f.unwatch, mqttrelay.D.WatchAvailability(func(c mqttrelay.AvailabilityChange) {
			if v, ok := availabilityValue(c.Current); ok {
				change := mqttrelay.DeviceChange{DeviceID: c.DeviceID, Name: c.Name, Values: map[string]any{"availability": v}}
				f.push(event{source: sourceDevice, device: &change, at: c.At})
			}
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
		conn, err := uuid.Parse(parts[1])
		if err != nil {
			return
		}
		var payload struct {
			NewState json.RawMessage `json:"new_state"`
		}
		if json.Unmarshal(m.Payload, &payload) != nil || len(payload.NewState) == 0 || string(payload.NewState) == "null" {
			return
		}
		var st haState
		if json.Unmarshal(payload.NewState, &st) != nil || st.EntityID == "" {
			return
		}
		f.push(event{source: sourceHA, haConn: conn, haState: &st, at: haTime(st.LastUpdated)})
	})
	f.unwatch = append(f.unwatch, func() { mq.B.Unsubscribe(sub) })
	go f.run()
	f.Reload()
}

// Stop unsubscribes and stops the worker. Points already handed to writers
// are theirs to deliver or spool.
func (f *Forwarder) Stop() {
	f.mu.Lock()
	if !f.started {
		f.mu.Unlock()
		return
	}
	f.started = false
	unwatch := f.unwatch
	f.unwatch = nil
	f.mu.Unlock()
	for _, u := range unwatch {
		u()
	}
	close(f.stop)
	<-f.done
}

// Reload rereads the forwards. Safe to call from any goroutine.
func (f *Forwarder) Reload() {
	select {
	case f.reload <- struct{}{}:
	default:
	}
}

// push queues an event without blocking the source.
func (f *Forwarder) push(e event) {
	select {
	case f.events <- e:
	default:
		metrics.MessagesDroppedTotal.WithLabelValues("history").Inc()
	}
}

func (f *Forwarder) run() {
	defer close(f.done)
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	lastRefresh, lastPrune := time.Time{}, time.Now()
	f.idx = &index{byID: map[uuid.UUID]*compiled{}}
	for {
		select {
		case <-f.stop:
			f.flushHeld(time.Now(), true)
			return
		case <-f.reload:
			f.loadIndex()
			f.readDests()
			lastRefresh = time.Now()
			f.prune(false)
			f.seed()
		case e := <-f.events:
			f.handle(e)
		case now := <-tick.C:
			if now.Sub(lastRefresh) >= refreshEvery {
				f.refreshDests()
				lastRefresh = now
			}
			if now.Sub(lastPrune) >= pruneEvery {
				f.prune(true)
				lastPrune = now
			}
			f.flushHeld(now, false)
			f.heartbeat(now)
		}
	}
}

// loadIndex compiles the enabled forwards.
func (f *Forwarder) loadIndex() {
	forwards, err := models.ListHistoryForwards()
	if err != nil {
		f.deps.Log.Error("could not load history forwards", zap.Error(err))
		return
	}
	idx := &index{
		controls: map[uuid.UUID][]*compiled{},
		devices:  map[uuid.UUID][]*compiled{},
		ha:       map[uuid.UUID][]*compiled{},
		byID:     map[uuid.UUID]*compiled{},
	}
	for _, fw := range forwards {
		if !fw.Enabled {
			continue
		}
		c := &compiled{f: fw, ids: map[uuid.UUID]bool{}, metric: fw.Metric}
		for _, id := range fw.SourceIDs.V {
			c.ids[id] = true
		}
		switch fw.SourceType {
		case models.ForwardSourceControl:
			if c.metric == "" {
				c.metric = metricControl
			}
			if len(c.ids) == 0 {
				idx.allControls = append(idx.allControls, c)
			}
			for id := range c.ids {
				idx.controls[id] = append(idx.controls[id], c)
			}
		case models.ForwardSourceMQTTDevice:
			if c.metric == "" {
				c.metric = metricDevice
			}
			if len(c.ids) == 0 {
				idx.allDevices = append(idx.allDevices, c)
			}
			for id := range c.ids {
				idx.devices[id] = append(idx.devices[id], c)
			}
		case models.ForwardSourceHA:
			if c.metric == "" {
				c.metric = metricHA
			}
			if fw.ConnectionID == nil {
				continue
			}
			c.include = globs(fw.Include.V)
			c.exclude = globs(fw.Exclude.V)
			idx.ha[*fw.ConnectionID] = append(idx.ha[*fw.ConnectionID], c)
		}
		idx.byID[fw.ID] = c
	}
	f.idx = idx
}

// globs compiles entity ID patterns, with * matching any run of characters.
func globs(patterns []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		parts := strings.Split(p, "*")
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		out = append(out, regexp.MustCompile("^"+strings.Join(parts, ".*")+"$"))
	}
	return out
}

func (c *compiled) matchesEntity(id string) bool {
	in := false
	for _, re := range c.include {
		if re.MatchString(id) {
			in = true
			break
		}
	}
	if !in {
		return false
	}
	for _, re := range c.exclude {
		if re.MatchString(id) {
			return false
		}
	}
	return true
}

// selects reports whether a forward still selects a source reference.
func (c *compiled) selects(source, ref string) bool {
	switch source {
	case sourceControl, sourceDevice:
		id, err := uuid.Parse(ref)
		return err == nil && (len(c.ids) == 0 || c.ids[id])
	case sourceHA:
		_, entity, ok := strings.Cut(ref, "/")
		return ok && c.matchesEntity(entity)
	}
	return false
}

// refreshDests rereads each destination's settings and seeds the heartbeats
// of one that has started asking for them. A connection that was not running
// when the forwards loaded, or whose heartbeat was set afterwards, would
// otherwise hear of a quiet series only at its next change.
func (f *Forwarder) refreshDests() {
	if f.readDests() {
		f.seed()
	}
}

// readDests rereads each destination's settings. It reports whether a
// destination now wants heartbeats that did not before.
func (f *Forwarder) readDests() bool {
	gained := false
	dests := map[uuid.UUID]destInfo{}
	for _, c := range f.idx.byID {
		for _, id := range c.f.Destinations.V {
			if _, ok := dests[id]; ok {
				continue
			}
			sink, err := f.deps.Sink(id)
			if err != nil {
				// Unreachable destinations still get points, which their
				// writer buffers; assume the conservative numeric-only shape
				// only once the sink can say.
				prev, had := f.dests[id]
				if had {
					dests[id] = prev
				} else {
					dests[id] = destInfo{}
				}
				continue
			}
			info := sink.SinkInfo()
			dests[id] = destInfo{numericOnly: info.NumericOnly, heartbeat: info.Heartbeat, ok: true}
			if info.Heartbeat > 0 && f.dests[id].heartbeat <= 0 {
				gained = true
			}
		}
	}
	f.dests = dests
	return gained
}

// handle matches one event against the forwards and writes its points.
func (f *Forwarder) handle(e event) {
	var matches []*compiled
	var samples []sample
	var source, ref string
	switch e.source {
	case sourceControl:
		id := e.control.Control.ID
		matches = append(append(matches, f.idx.controls[id]...), f.idx.allControls...)
		if len(matches) == 0 {
			return
		}
		samples = controlSamples(*e.control)
		source, ref = sourceControl, id.String()
	case sourceDevice:
		id := e.device.DeviceID
		matches = append(append(matches, f.idx.devices[id]...), f.idx.allDevices...)
		if len(matches) == 0 {
			return
		}
		samples = deviceSamples(id.String(), e.device.Name, e.device.Kind, e.device.Values, e.at)
		source, ref = sourceDevice, id.String()
	case sourceHA:
		source, ref = sourceHA, e.haConn.String()+"/"+e.haState.EntityID
		for _, c := range f.idx.ha[e.haConn] {
			if c.matchesEntity(e.haState.EntityID) {
				matches = append(matches, c)
			}
		}
		if len(matches) == 0 {
			return
		}
		st := e.haState
		if st.State == "unavailable" || st.State == "unknown" {
			f.stopSource(source, ref)
			return
		}
		name := e.haConn.String()
		if f.deps.ConnectionName != nil {
			name = f.deps.ConnectionName(e.haConn)
		}
		// Forwards may ask for different attributes, so each builds its own
		// samples.
		f.emit(matches, source, ref, func(c *compiled) []sample {
			return haSamples(name, st.EntityID, st.State, st.Attributes, e.at, c.f.Attributes.V, c.f.AllAttributes)
		})
		return
	}
	f.emit(matches, source, ref, func(*compiled) []sample { return samples })
}

// emit shapes samples for every destination of the matching forwards and
// hands them to the writers, writing each series once per destination.
func (f *Forwarder) emit(matches []*compiled, source, ref string, samplesFor func(*compiled) []sample) {
	byDest := map[uuid.UUID][]tsdb.Point{}
	seen := map[string]bool{}
	nextPrev := map[string]string{}
	for _, c := range matches {
		samples := samplesFor(c)
		for _, dest := range c.f.Destinations.V {
			info := f.dests[dest]
			for _, s := range samples {
				prevKey := dest.String() + "|" + s.series
				points := shape(s, c.metric, c.f.Labels.V, info.numericOnly, f.enumPrev[prevKey].value)
				if s.kind == valEnum {
					nextPrev[prevKey] = s.text
				}
				for _, p := range points {
					key := seriesKey(dest.String(), p)
					if seen[key] {
						continue
					}
					seen[key] = true
					if info.heartbeat > 0 {
						f.setBeat(key, &beat{dest: dest, forward: c.f.ID, source: source, ref: ref, ident: identity(dest, c.f.ID, s, p), point: p, sentAt: time.UnixMilli(p.Time)})
					}
					if c.f.MinIntervalMS > 0 {
						if last, ok := f.lastSent[key]; ok && time.UnixMilli(p.Time).Sub(last) < time.Duration(c.f.MinIntervalMS)*time.Millisecond {
							f.held[key] = pending{dest: dest, point: p, interval: time.Duration(c.f.MinIntervalMS) * time.Millisecond}
							continue
						}
						f.lastSent[key] = time.UnixMilli(p.Time)
						// A value sent now supersedes any older one still held.
						delete(f.held, key)
					}
					byDest[dest] = append(byDest[dest], p)
				}
			}
		}
	}
	now := time.Now()
	for k, v := range nextPrev {
		f.enumPrev[k] = enumState{value: v, at: now}
	}
	f.write(byDest)
}

func (f *Forwarder) write(byDest map[uuid.UUID][]tsdb.Point) {
	for dest, points := range byDest {
		if len(points) == 0 {
			continue
		}
		w, err := f.deps.Writer(dest)
		if err != nil {
			f.deps.Log.Error("no history writer for destination", zap.String("connection", dest.String()), zap.Error(err))
			continue
		}
		w.Enqueue(points)
	}
}

// flushHeld releases values held by a minimum interval once it has passed,
// or all of them when stopping.
func (f *Forwarder) flushHeld(now time.Time, all bool) {
	if len(f.held) == 0 {
		return
	}
	byDest := map[uuid.UUID][]tsdb.Point{}
	for key, h := range f.held {
		if !all && now.Sub(f.lastSent[key]) < h.interval {
			continue
		}
		byDest[h.dest] = append(byDest[h.dest], h.point)
		f.lastSent[key] = time.UnixMilli(h.point.Time)
		delete(f.held, key)
	}
	f.write(byDest)
}

// heartbeat re-sends series whose destination heartbeat is due.
func (f *Forwarder) heartbeat(now time.Time) {
	byDest := map[uuid.UUID][]tsdb.Point{}
	for key, b := range f.beats {
		info := f.dests[b.dest]
		if info.heartbeat <= 0 || now.Sub(b.sentAt) < info.heartbeat {
			continue
		}
		// A value held by a minimum interval has not been sent yet;
		// re-sending it now would put it ahead of its own original.
		if _, held := f.held[key]; held {
			continue
		}
		p := b.point
		p.Time = now.UnixMilli()
		b.point = p
		b.sentAt = now
		byDest[b.dest] = append(byDest[b.dest], p)
	}
	f.write(byDest)
}

// stopSource ends the heartbeats of one source, when it stops reporting a
// recordable value.
func (f *Forwarder) stopSource(source, ref string) {
	for k, b := range f.beats {
		if b.source == source && b.ref == ref {
			f.deleteBeat(k)
		}
	}
}

// identity names a series apart from its labels: destination, forward,
// source attribute, and state-set option. Names and units change; this
// does not.
func identity(dest, forward uuid.UUID, s sample, p tsdb.Point) string {
	return dest.String() + "|" + forward.String() + "|" + s.series + "|" + p.Labels["state"]
}

// setBeat records a series' last point for heartbeats, replacing the beat
// of the same series under labels it no longer has.
func (f *Forwarder) setBeat(key string, b *beat) {
	if old, ok := f.identKey[b.ident]; ok && old != key {
		f.deleteBeat(old)
		delete(f.lastSent, old)
		delete(f.held, old)
	}
	f.identKey[b.ident] = key
	f.beats[key] = b
}

// deleteBeat stops a series' heartbeat.
func (f *Forwarder) deleteBeat(key string) {
	if b, ok := f.beats[key]; ok && f.identKey[b.ident] == key {
		delete(f.identKey, b.ident)
	}
	delete(f.beats, key)
}

// prune drops heartbeats that no forward selects any more, and with
// sources set, those whose control, device, or entity is gone.
func (f *Forwarder) prune(sources bool) {
	var haPresent map[string]bool
	if sources && f.deps.HAStates != nil {
		haPresent = map[string]bool{}
		conns := map[uuid.UUID]bool{}
		for _, b := range f.beats {
			if b.source == sourceHA {
				connText, _, _ := strings.Cut(b.ref, "/")
				if id, err := uuid.Parse(connText); err == nil {
					conns[id] = true
				}
			}
		}
		for id := range conns {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			states, err := f.deps.HAStates(ctx, id)
			cancel()
			if err != nil {
				// An unreachable Home Assistant says nothing about which
				// entities exist; keep its heartbeats.
				for _, b := range f.beats {
					if strings.HasPrefix(b.ref, id.String()+"/") {
						haPresent[b.ref] = true
					}
				}
				continue
			}
			for _, s := range states {
				if s.State != "unavailable" && s.State != "unknown" {
					haPresent[id.String()+"/"+s.EntityID] = true
				}
			}
		}
	}
	for k, b := range f.beats {
		c, ok := f.idx.byID[b.forward]
		keep := ok && c.selects(b.source, b.ref) && containsID(c.f.Destinations.V, b.dest)
		if keep && sources {
			switch b.source {
			case sourceControl:
				id, _ := uuid.Parse(b.ref)
				_, keep = controls.S.Get(id)
			case sourceDevice:
				id, _ := uuid.Parse(b.ref)
				if mqttrelay.D != nil {
					_, keep = mqttrelay.D.State(id)
				}
			case sourceHA:
				if haPresent != nil {
					keep = haPresent[b.ref]
				}
			}
		}
		if !keep {
			f.deleteBeat(k)
		}
	}
	// Bookkeeping for series that have gone quiet no longer changes any
	// decision once it is older than the longest minimum interval.
	cutoff := time.Now().Add(-staleAfter)
	for k, t := range f.lastSent {
		if _, held := f.held[k]; !held && t.Before(cutoff) {
			delete(f.lastSent, k)
		}
	}
	for k, e := range f.enumPrev {
		if e.at.Before(cutoff) {
			delete(f.enumPrev, k)
		}
	}
}

// seed gives heartbeat destinations the current value of every selected
// control, device, and entity, so a series exists before its first change.
// Destinations without a heartbeat get nothing, so a reload never writes
// duplicate points to them.
func (f *Forwarder) seed() {
	now := time.Now()
	wantsBeat := func(c *compiled) bool {
		for _, d := range c.f.Destinations.V {
			if f.dests[d].heartbeat > 0 {
				return true
			}
		}
		return false
	}
	seedFrom := func(c *compiled, source, ref string, samples []sample) {
		for _, dest := range c.f.Destinations.V {
			info := f.dests[dest]
			if info.heartbeat <= 0 {
				continue
			}
			for _, s := range samples {
				s.at = now
				for _, p := range shape(s, c.metric, c.f.Labels.V, info.numericOnly, "") {
					key := seriesKey(dest.String(), p)
					if _, ok := f.beats[key]; ok {
						continue
					}
					// sentAt in the past makes the next tick send it.
					f.setBeat(key, &beat{dest: dest, forward: c.f.ID, source: source, ref: ref, ident: identity(dest, c.f.ID, s, p), point: p, sentAt: time.Time{}})
				}
			}
		}
	}
	for _, c := range f.idx.byID {
		if !wantsBeat(c) {
			continue
		}
		switch c.f.SourceType {
		case models.ForwardSourceControl:
			for _, snap := range controls.S.List() {
				if len(c.ids) > 0 && !c.ids[snap.ID] {
					continue
				}
				seedFrom(c, sourceControl, snap.ID.String(), controlSamples(controls.Change{Control: snap, At: now}))
			}
		case models.ForwardSourceMQTTDevice:
			if mqttrelay.D == nil {
				continue
			}
			devices, err := models.ListMQTTDevices()
			if err != nil {
				continue
			}
			for _, d := range devices {
				if len(c.ids) > 0 && !c.ids[d.ID] {
					continue
				}
				state, ok := mqttrelay.D.State(d.ID)
				if !ok || len(state) == 0 {
					continue
				}
				seedFrom(c, sourceDevice, d.ID.String(), deviceSamples(d.ID.String(), d.Name, d.Control, state, now))
			}
		case models.ForwardSourceHA:
			if c.f.ConnectionID == nil || f.deps.HAStates == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			states, err := f.deps.HAStates(ctx, *c.f.ConnectionID)
			cancel()
			if err != nil {
				continue
			}
			name := c.f.ConnectionID.String()
			if f.deps.ConnectionName != nil {
				name = f.deps.ConnectionName(*c.f.ConnectionID)
			}
			for _, s := range states {
				if !c.matchesEntity(s.EntityID) {
					continue
				}
				ref := c.f.ConnectionID.String() + "/" + s.EntityID
				seedFrom(c, sourceHA, ref, haSamples(name, s.EntityID, s.State, s.Attributes, now, c.f.Attributes.V, c.f.AllAttributes))
			}
		}
	}
}

func containsID(list []uuid.UUID, id uuid.UUID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// haTime reads Home Assistant's last_updated, falling back to now.
func haTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Now()
}
