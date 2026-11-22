// Package frigate connects to a Frigate NVR server over its HTTP API and
// websocket: its cameras, their snapshots and live streams, the events and
// review items it detects, and the camera switches it exposes. Karakuri
// stores no video; every image and clip is fetched from Frigate on request.
package frigate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"go.uber.org/zap"
)

// Kind is the connection kind identifier.
const Kind = "frigate"

// Session timing and limits.
const (
	backoffMin     = time.Second
	backoffMax     = 60 * time.Second
	maxConfigBytes = 8 << 20
	maxStatsBytes  = 4 << 20
	maxMessage     = 4 << 20
)

// statsInterval and pingInterval are variables so tests can shorten them.
var (
	statsInterval = 15 * time.Second
	pingInterval  = 30 * time.Second
)

// Features are the camera switches a device can follow.
var Features = []string{"detect", "record", "snapshots", "motion"}

// camera is one camera's live view in the driver.
type camera struct {
	name      string
	zones     []string
	objects   []string
	receiving bool
	switches  map[string]bool
}

// Driver is one Frigate server connection.
type Driver struct {
	id      uuid.UUID
	name    string
	cfg     Config
	client  *client
	log     *zap.Logger
	publish func(integrations.Status)
	streams *streamHub

	mu      sync.Mutex
	status  integrations.Status
	version string
	cameras map[string]*camera
	conn    *websocket.Conn

	watchMu     sync.Mutex
	nextWatch   int
	eventWatch  map[int]func(nvr.Event)
	reviewWatch map[int]func(nvr.Review)
	entityWatch map[string]map[int]func(map[string]any)
	availWatch  map[string]map[int]func(bool)
	listWatch   map[int]func()

	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a driver for one connection.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := decodeConfig(model.Config, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	d := &Driver{
		id:          model.ID,
		name:        model.Name,
		cfg:         cfg,
		client:      newClient(cfg),
		log:         log,
		publish:     deps.PublishStatus,
		status:      integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		cameras:     map[string]*camera{},
		eventWatch:  map[int]func(nvr.Event){},
		reviewWatch: map[int]func(nvr.Review){},
		entityWatch: map[string]map[int]func(map[string]any){},
		availWatch:  map[string]map[int]func(bool){},
		listWatch:   map[int]func(){},
	}
	d.streams = newStreamHub(d.openStream, d.log)
	return d, nil
}

// ID returns the connection ID.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Name returns the connection's name.
func (d *Driver) Name() string { return d.name }

// Status returns the connection's state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Version returns the Frigate version of the current session.
func (d *Driver) Version() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

// setStatus records and relays a state change, collapsing repeats.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	d.mu.Unlock()
	d.log.Info("frigate connection status", zap.String("state", state), zap.String("error", errMsg))
	if d.publish != nil {
		d.publish(status)
	}
}

// Start begins connecting and supervises reconnects until Stop.
func (d *Driver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.run(ctx)
	return nil
}

// Stop ends the session and every shared stream.
func (d *Driver) Stop() {
	if d.cancel != nil {
		d.cancel()
		<-d.done
	}
	d.streams.closeAll()
	d.setStatus(integrations.StateDisabled, "")
}

// run keeps a session up, backing off between attempts.
func (d *Driver) run(ctx context.Context) {
	defer close(d.done)
	backoff := backoffMin
	for {
		started := time.Now()
		err := d.session(ctx)
		d.markAllNotReceiving()
		if ctx.Err() != nil {
			return
		}
		d.setStatus(integrations.StateError, err.Error())
		if time.Since(started) > 2*backoffMax {
			backoff = backoffMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// session runs one connection to Frigate until it fails.
func (d *Driver) session(ctx context.Context) error {
	d.setStatus(integrations.StateConnecting, "")
	if err := d.client.login(ctx); err != nil {
		return err
	}
	version, err := d.client.version(ctx)
	if err != nil {
		return err
	}
	if err := d.loadConfig(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	d.version = version
	d.mu.Unlock()

	conn, err := d.dialWS(ctx)
	if err != nil {
		return err
	}
	conn.SetReadLimit(maxMessage)
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.conn = nil
		d.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	if err := wsjson.Write(ctx, conn, map[string]string{"topic": "onConnect"}); err != nil {
		return fmt.Errorf("frigate websocket: %w", err)
	}
	if err := d.pollStats(ctx); err != nil {
		return err
	}
	d.setStatus(integrations.StateConnected, "")
	d.announceReceiving()
	d.announceConfigSwitches()
	d.notifyList()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- d.readLoop(sessionCtx, conn) }()
	go func() { errc <- d.keepalive(sessionCtx, conn) }()
	return <-errc
}

// dialWS opens Frigate's websocket with the session cookie.
func (d *Driver) dialWS(ctx context.Context) (*websocket.Conn, error) {
	u, _ := url.Parse(d.cfg.base() + "/ws")
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	header := http.Header{}
	if ck := d.client.cookie(); ck != "" {
		header.Set("Cookie", ck)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{
		HTTPClient: d.client.http, HTTPHeader: header,
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("frigate websocket: login refused")
		}
		return nil, fmt.Errorf("frigate websocket: %w", err)
	}
	return conn, nil
}

// keepalive pings the websocket and polls stats until the session ends.
func (d *Driver) keepalive(ctx context.Context, conn *websocket.Conn) error {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	stats := time.NewTicker(statsInterval)
	defer stats.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return fmt.Errorf("frigate stopped answering: %w", err)
			}
		case <-stats.C:
			if err := d.pollStats(ctx); err != nil {
				return err
			}
		}
	}
}

// wsMessage is one websocket message. Frigate sends payloads as strings,
// JSON-encoded for structured topics.
type wsMessage struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

// payloadText reads a message's payload as the string Frigate sent.
func (m wsMessage) payloadText() string {
	var s string
	if json.Unmarshal(m.Payload, &s) == nil {
		return s
	}
	return string(m.Payload)
}

// readLoop dispatches websocket messages until the connection fails.
func (d *Driver) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		var msg wsMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return fmt.Errorf("frigate websocket: %w", err)
		}
		d.dispatch(msg)
	}
}

// dispatch handles one websocket message.
func (d *Driver) dispatch(msg wsMessage) {
	switch msg.Topic {
	case "camera_activity":
		d.applyActivity(msg.payloadText())
		return
	case "events":
		var m changeMessage
		if json.Unmarshal([]byte(msg.payloadText()), &m) != nil || m.After == nil {
			return
		}
		e := d.eventFrom(*m.After, m.Before, m.Type)
		d.notifyEvent(e)
		return
	case "reviews":
		var m reviewMessage
		if json.Unmarshal([]byte(msg.payloadText()), &m) != nil || m.After == nil {
			return
		}
		d.notifyReview(reviewFrom(*m.After, m.Type))
		return
	}
	// <camera>/<feature>/state reports a switch changing.
	parts := strings.Split(msg.Topic, "/")
	if len(parts) == 3 && parts[2] == "state" && isFeature(parts[1]) {
		on := strings.EqualFold(msg.payloadText(), "ON")
		d.setSwitch(parts[0], parts[1], on)
	}
}

// isFeature reports whether a name is a switch devices follow.
func isFeature(name string) bool {
	for _, f := range Features {
		if f == name {
			return true
		}
	}
	return false
}

// activityFeatures are the switches camera_activity carries, which Frigate
// sends in answer to onConnect.
var activityFeatures = []string{"detect", "record", "snapshots"}

// announceConfigSwitches tells entity watchers the switches no
// camera_activity message carries, after a connect. Their values come from
// the config alone, so a watcher that registered before the connection was up,
// or whose switch changed while it was down, would otherwise hear nothing
// until the switch next changed.
func (d *Driver) announceConfigSwitches() {
	for _, cam := range d.Cameras() {
		for _, f := range Features {
			if slices.Contains(activityFeatures, f) {
				continue
			}
			if on, ok := cam.Switches[f]; ok {
				d.notifyEntity(cam.Name+"/"+f, map[string]any{"on": on})
			}
		}
	}
}

// applyActivity reads camera_activity, which carries each camera's switches.
// Motion's switch is not in it; that one comes from the config and from
// motion/state messages.
func (d *Driver) applyActivity(payload string) {
	var activity map[string]struct {
		Config map[string]any `json:"config"`
	}
	if json.Unmarshal([]byte(payload), &activity) != nil {
		return
	}
	for name, a := range activity {
		for _, f := range activityFeatures {
			if v, ok := a.Config[f].(bool); ok {
				d.setSwitch(name, f, v)
			}
		}
	}
}

// setSwitch records one switch and tells the entity's watchers.
func (d *Driver) setSwitch(cameraName, feature string, on bool) {
	d.mu.Lock()
	cam, ok := d.cameras[cameraName]
	if !ok {
		d.mu.Unlock()
		return
	}
	cam.switches[feature] = on
	d.mu.Unlock()
	d.notifyEntity(cameraName+"/"+feature, map[string]any{"on": on})
}

// frigateConfig is the part of /api/config the driver reads.
type frigateConfig struct {
	Cameras map[string]struct {
		Enabled *bool               `json:"enabled"`
		Zones   map[string]struct{} `json:"zones"`
		Objects struct {
			Track []string `json:"track"`
		} `json:"objects"`
		Motion struct {
			Enabled *bool `json:"enabled"`
		} `json:"motion"`
		Detect struct {
			Enabled *bool `json:"enabled"`
		} `json:"detect"`
		Record struct {
			Enabled *bool `json:"enabled"`
		} `json:"record"`
		Snapshots struct {
			Enabled *bool `json:"enabled"`
		} `json:"snapshots"`
	} `json:"cameras"`
}

// loadConfig reads the cameras from Frigate's config, keeping the receiving
// state and switches of cameras that stay.
func (d *Driver) loadConfig(ctx context.Context) error {
	var cfg frigateConfig
	if err := d.client.getJSON(ctx, "/api/config", maxConfigBytes, &cfg); err != nil {
		return err
	}
	cams := map[string]*camera{}
	for name, c := range cfg.Cameras {
		cam := &camera{name: name, objects: c.Objects.Track, switches: map[string]bool{}}
		for z := range c.Zones {
			cam.zones = append(cam.zones, z)
		}
		sort.Strings(cam.zones)
		for f, v := range map[string]*bool{"detect": c.Detect.Enabled, "record": c.Record.Enabled,
			"snapshots": c.Snapshots.Enabled, "motion": c.Motion.Enabled} {
			cam.switches[f] = v == nil || *v
		}
		if f := cam.objects; f == nil {
			cam.objects = []string{}
		}
		cams[name] = cam
	}
	d.mu.Lock()
	for name, cam := range cams {
		if old, ok := d.cameras[name]; ok {
			cam.receiving = old.receiving
		}
	}
	d.cameras = cams
	d.mu.Unlock()
	return nil
}

// pollStats reads which cameras receive frames and reloads the config when
// Frigate reports a camera the driver does not know.
func (d *Driver) pollStats(ctx context.Context) error {
	var stats struct {
		Cameras map[string]struct {
			CameraFPS float64 `json:"camera_fps"`
		} `json:"cameras"`
	}
	if err := d.client.getJSON(ctx, "/api/stats", maxStatsBytes, &stats); err != nil {
		return fmt.Errorf("reading frigate stats: %w", err)
	}
	d.mu.Lock()
	changed := len(stats.Cameras) != len(d.cameras)
	for name := range stats.Cameras {
		if _, ok := d.cameras[name]; !ok {
			changed = true
		}
	}
	d.mu.Unlock()
	if changed {
		if err := d.loadConfig(ctx); err != nil {
			return err
		}
		d.notifyList()
	}

	type flip struct {
		name string
		on   bool
	}
	var flips []flip
	d.mu.Lock()
	for name, cam := range d.cameras {
		receiving := stats.Cameras[name].CameraFPS > 0
		if receiving != cam.receiving {
			cam.receiving = receiving
			flips = append(flips, flip{name, receiving})
		}
	}
	d.mu.Unlock()
	for _, f := range flips {
		d.log.Info("frigate camera frames", zap.String("camera", f.name), zap.Bool("receiving", f.on))
		d.notifyAvailability(f.name, f.on)
	}
	return nil
}

// markAllNotReceiving clears receiving when the session ends, since nothing
// can be fetched from a server the driver cannot reach. Watchers are not told:
// the connection's status says that, and a camera's own state is announced
// again once the connection is back.
func (d *Driver) markAllNotReceiving() {
	d.mu.Lock()
	for _, cam := range d.cameras {
		cam.receiving = false
	}
	d.mu.Unlock()
}

// announceReceiving tells availability watchers every camera's state, after
// a connect.
func (d *Driver) announceReceiving() {
	for _, cam := range d.Cameras() {
		d.notifyAvailability(cam.Name, cam.Receiving)
	}
}

// Cameras returns the cameras by name.
func (d *Driver) Cameras() []nvr.Camera {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]nvr.Camera, 0, len(d.cameras))
	for _, c := range d.cameras {
		sw := make(map[string]bool, len(c.switches))
		for k, v := range c.switches {
			sw[k] = v
		}
		out = append(out, nvr.Camera{Name: c.name, Zones: append([]string{}, c.zones...),
			Objects: append([]string{}, c.objects...), Receiving: c.receiving, Switches: sw})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Receiving reports whether a camera received frames at the last check.
func (d *Driver) Receiving(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cameras[name]
	return ok && c.receiving
}

// hasCamera reports whether the server has a camera.
func (d *Driver) hasCamera(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.cameras[name]
	return ok
}

// ready checks that a camera exists and is receiving frames.
func (d *Driver) ready(name string) error {
	if d.Status().State != integrations.StateConnected {
		return fmt.Errorf("frigate is not connected: %s", d.Status().Error)
	}
	if !d.hasCamera(name) {
		return &nvr.UnknownCameraError{Camera: name}
	}
	if !d.Receiving(name) {
		return &nvr.NotReceivingError{Camera: name}
	}
	return nil
}

// sendSwitch sets one camera switch over the websocket. Frigate answers with
// a state message, which is what changes the device.
func (d *Driver) sendSwitch(ctx context.Context, cameraName, feature string, on bool) error {
	if !isFeature(feature) {
		return fmt.Errorf("frigate cameras have no %q switch", feature)
	}
	if !d.hasCamera(cameraName) {
		return &nvr.UnknownCameraError{Camera: cameraName}
	}
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	if conn == nil {
		return errors.New("frigate is not connected")
	}
	payload := "OFF"
	if on {
		payload = "ON"
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(wctx, conn, map[string]string{"topic": cameraName + "/" + feature + "/set", "payload": payload})
}

// WatchEvents calls fn for every event message.
func (d *Driver) WatchEvents(fn func(nvr.Event)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.eventWatch[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.eventWatch, id)
		d.watchMu.Unlock()
	}
}

// WatchReviews calls fn for every review item message.
func (d *Driver) WatchReviews(fn func(nvr.Review)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.reviewWatch[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.reviewWatch, id)
		d.watchMu.Unlock()
	}
}

// WatchCameraList calls fn after every connect and every camera list change.
func (d *Driver) WatchCameraList(fn func()) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.listWatch[id] = fn
	d.watchMu.Unlock()
	if d.Status().State == integrations.StateConnected {
		fn()
	}
	return func() {
		d.watchMu.Lock()
		delete(d.listWatch, id)
		d.watchMu.Unlock()
	}
}

func (d *Driver) notifyEvent(e nvr.Event) {
	d.watchMu.Lock()
	fns := make([]func(nvr.Event), 0, len(d.eventWatch))
	for _, fn := range d.eventWatch {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(e)
	}
}

func (d *Driver) notifyReview(r nvr.Review) {
	d.watchMu.Lock()
	fns := make([]func(nvr.Review), 0, len(d.reviewWatch))
	for _, fn := range d.reviewWatch {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(r)
	}
}

func (d *Driver) notifyEntity(objectID string, values map[string]any) {
	d.watchMu.Lock()
	fns := make([]func(map[string]any), 0, len(d.entityWatch[objectID]))
	for _, fn := range d.entityWatch[objectID] {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(map[string]any{"on": values["on"]})
	}
}

func (d *Driver) notifyAvailability(cameraName string, receiving bool) {
	d.watchMu.Lock()
	var fns []func(bool)
	for objectID, set := range d.availWatch {
		if strings.HasPrefix(objectID, cameraName+"/") {
			for _, fn := range set {
				fns = append(fns, fn)
			}
		}
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(receiving)
	}
}

func (d *Driver) notifyList() {
	d.watchMu.Lock()
	fns := make([]func(), 0, len(d.listWatch))
	for _, fn := range d.listWatch {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// SetTimingForTest shortens the stats and ping intervals and returns the
// function that restores them. Only tests call it.
func SetTimingForTest(stats, ping time.Duration) (restore func()) {
	oldStats, oldPing := statsInterval, pingInterval
	statsInterval, pingInterval = stats, ping
	return func() { statsInterval, pingInterval = oldStats, oldPing }
}

var _ nvr.NVR = (*Driver)(nil)
