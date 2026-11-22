// Package openhab implements the openHAB connection driver over openHAB's
// REST API and its server-sent event stream: an item copy kept current from
// item events, state changes fanned out to devices and triggers, commands,
// and a catch-up after the stream drops.
package openhab

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"go.uber.org/zap"
)

// Kind is the driver kind registered with the integrations registry.
const Kind = "openhab"

// Timing defaults; driver fields so tests can shorten them.
const (
	requestTimeout = 15 * time.Second
	// aliveTimeout is how long the stream may stay silent. openHAB sends an
	// alive event every 10 s, so 30 s of nothing means the stream is dead
	// even when the socket has not noticed.
	aliveTimeout   = 30 * time.Second
	defaultBackoff = time.Second
	maxBackoff     = 30 * time.Second
	// rereadDebounce bounds how often item additions, updates, and removals
	// reread the item list: loading an items file fires one event per item.
	rereadDebounce = time.Second
	eventQueueSize = 4096
)

// eventTopics are the events the driver follows.
const eventTopics = "openhab/items/*/statechanged,openhab/items/*/added,openhab/items/*/removed,openhab/items/*/updated"

// Config is the driver configuration stored (encrypted) on a connection.
type Config struct {
	BaseURL string `json:"base_url"`
	// Token is an optional API token; openHAB lets anonymous clients read
	// and command by default.
	Token string `json:"token,omitempty"`
}

// root is the server address without a trailing slash or /rest.
func (c Config) root() string {
	u := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	u = strings.TrimSuffix(u, "/rest")
	return strings.TrimRight(u, "/")
}

// Validate checks that the base URL is usable.
func (c Config) Validate() error {
	root := c.root()
	if root == "" {
		return fmt.Errorf("openhab config has no base_url")
	}
	u, err := url.Parse(root)
	if err != nil || u.Host == "" {
		return fmt.Errorf("base_url %q is not a URL such as http://openhab.local:8080", c.BaseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url must start with http:// or https://")
	}
	return nil
}

// ItemChange is one item state change, live or caught up after a reconnect.
type ItemChange struct {
	Name string
	// Old and New are the state strings; empty for an item that appeared or
	// disappeared during an outage.
	Old  string
	New  string
	Item Item
	// CaughtUp marks a change found by the read after a reconnect: the
	// latest state, not each transition the outage hid.
	CaughtUp bool
}

// Driver is a running openHAB connection shared by every node and device
// referencing it.
type Driver struct {
	id     uuid.UUID
	cfg    Config
	client *http.Client
	log    *zap.Logger

	publishStatus func(integrations.Status)

	aliveTimeout   time.Duration
	initialBackoff time.Duration
	rereadDebounce time.Duration

	mu         sync.Mutex
	status     integrations.Status
	version    string
	items      Catalog
	ctx        context.Context
	cancel     context.CancelFunc
	generation uint64
	reread     *time.Timer

	watchMu      sync.Mutex
	handlers     map[int]func(ItemChange)
	itemWatchers map[int]func(Catalog)
	nextWatch    int

	memory *stateMemory
}

// New builds an openHAB driver from a connection model. Registered as the
// factory for the "openhab" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid openhab config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:             model.ID,
		cfg:            cfg,
		client:         &http.Client{},
		log:            log,
		publishStatus:  deps.PublishStatus,
		aliveTimeout:   aliveTimeout,
		initialBackoff: defaultBackoff,
		rereadDebounce: rereadDebounce,
		status:         integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		items:          Catalog{},
		handlers:       map[int]func(ItemChange){},
		itemWatchers:   map[int]func(Catalog){},
		memory:         memoryFor(model.ID.String() + "|" + cfg.root()),
	}, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the connection's lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Version is the openHAB version the server reported, empty before the first
// connect.
func (d *Driver) Version() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	publish := d.publishStatus
	d.mu.Unlock()
	d.log.Info("openhab connection status", zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start launches the reconnect supervisor.
func (d *Driver) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.ctx, d.cancel = runCtx, cancel
	d.mu.Unlock()
	d.setStatus(integrations.StateConnecting, "")
	go d.supervise(runCtx)
	return nil
}

// Stop ends the connection and marks it disabled.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	if d.reread != nil {
		d.reread.Stop()
	}
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// supervise runs sessions until the context ends, backing off from 1 s
// doubling to 30 s between failed ones; a session that connected resets it.
func (d *Driver) supervise(ctx context.Context) {
	backoff := d.initialBackoff
	for {
		connected, err := d.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = d.initialBackoff
			metrics.ConnectionReconnectsTotal.WithLabelValues(Kind).Inc()
		}
		msg := "event stream ended"
		if err != nil {
			msg = err.Error()
		}
		d.setStatus(integrations.StateError, msg)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// sseEvent is one message of the event stream.
type sseEvent struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	Type    string `json:"type"`
}

// session runs one connection: read the version, open the event stream, read
// every item, catch up, then follow the stream until it ends or goes silent.
// The stream is opened before the item read so nothing falls between them.
func (d *Driver) session(ctx context.Context) (bool, error) {
	version, err := d.readVersion(ctx)
	if err != nil {
		return false, err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(sessCtx, http.MethodGet,
		d.cfg.root()+"/rest/events?topics="+url.QueryEscape(eventTopics), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	d.authorize(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("opening the event stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, statusError("opening the event stream", resp)
	}

	events := make(chan sseEvent, eventQueueSize)
	alive := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	go func() { readErr <- readStream(sessCtx, resp.Body, events, alive) }()

	items, err := d.readItems(sessCtx)
	if err != nil {
		return false, err
	}
	d.mu.Lock()
	d.version = version
	d.items = items
	d.generation++
	d.mu.Unlock()
	d.notifyItems()
	d.catchUp(items)
	d.setStatus(integrations.StateConnected, "")
	d.log.Debug("openhab session established", zap.String("version", version), zap.Int("items", len(items)))

	silence := time.NewTimer(d.aliveTimeout)
	defer silence.Stop()
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case err := <-readErr:
			if err == nil {
				err = fmt.Errorf("event stream ended")
			}
			return true, err
		case <-silence.C:
			return true, fmt.Errorf("event stream silent for %s", d.aliveTimeout)
		case <-alive:
			silence.Reset(d.aliveTimeout)
		case ev := <-events:
			silence.Reset(d.aliveTimeout)
			d.handle(ev)
		}
	}
}

// readStream reads server-sent events: event and data lines, a blank line
// ending each. Message events go to events; any event, the keepalive
// included, signals alive. It stops when ctx ends, so a session that returns
// with the queue full does not leave it blocked.
func readStream(ctx context.Context, body io.Reader, events chan<- sseEvent, alive chan<- struct{}) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var name string
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data.Len() > 0 || name != "" {
				select {
				case alive <- struct{}{}:
				default:
				}
				if name == "" || name == "message" {
					var ev sseEvent
					if json.Unmarshal([]byte(data.String()), &ev) == nil && ev.Topic != "" {
						select {
						case events <- ev:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
			}
			name = ""
			data.Reset()
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return sc.Err()
}

// handle applies one event to the item copy.
func (d *Driver) handle(ev sseEvent) {
	parts := strings.Split(ev.Topic, "/")
	if len(parts) < 4 || parts[0] != "openhab" || parts[1] != "items" {
		return
	}
	name, kind := parts[2], parts[3]
	// A group's own state changes arrive only as
	// openhab/items/<group>/<member>/statechanged, naming the member whose
	// change moved the group's state.
	if len(parts) == 5 && parts[4] == "statechanged" {
		kind = "statechanged"
	} else if len(parts) != 4 {
		return
	}
	switch kind {
	case "statechanged":
		var p struct {
			Value    string `json:"value"`
			OldValue string `json:"oldValue"`
		}
		if json.Unmarshal([]byte(ev.Payload), &p) != nil {
			return
		}
		d.mu.Lock()
		it, ok := d.items[name]
		if !ok || it.State == p.Value {
			// An item not in the copy is announced by its added event and
			// the reread after it; a state the copy already holds was
			// reported by the read that found it.
			d.mu.Unlock()
			return
		}
		it.State = p.Value
		d.items[name] = it
		d.mu.Unlock()
		d.memory.remember(name, p.Value)
		d.fanOut(ItemChange{Name: name, Old: p.OldValue, New: p.Value, Item: it})
	case "added", "removed", "updated":
		// Event payloads carry no semantic metadata, so the list is reread
		// rather than patched.
		d.scheduleReread()
	}
}

// scheduleReread rereads the item list once additions and removals settle.
func (d *Driver) scheduleReread() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reread != nil {
		d.reread.Stop()
	}
	ctx := d.ctx
	d.reread = time.AfterFunc(d.rereadDebounce, func() {
		if ctx == nil || ctx.Err() != nil {
			return
		}
		items, err := d.readItems(ctx)
		if err != nil {
			d.log.Warn("openhab item reread failed", zap.Error(err))
			return
		}
		// The stream reported every change to an item already in the copy,
		// possibly after this read, so the copy's state stands. An item new
		// to the copy had its changes dropped until now, so the read's state
		// is reported against the last one seen, as when an item is removed
		// and added back with another state.
		var appeared []Item
		d.mu.Lock()
		for name, it := range items {
			if prev, ok := d.items[name]; ok {
				it.State = prev.State
				items[name] = it
			} else {
				appeared = append(appeared, it)
			}
		}
		d.items = items
		d.mu.Unlock()
		for _, it := range appeared {
			if old, known := d.memory.swap(it.Name, it.State); known && old != it.State {
				d.fanOut(ItemChange{Name: it.Name, Old: old, New: it.State, Item: it})
			}
		}
		d.notifyItems()
	})
}

// Items returns a copy of the item catalog.
func (d *Driver) Items() Catalog {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(Catalog, len(d.items))
	for k, v := range d.items {
		out[k] = v
	}
	return out
}

// Item returns one item.
func (d *Driver) Item(name string) (Item, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	it, ok := d.items[name]
	return it, ok
}

// Connected reports whether the driver holds a current item copy.
func (d *Driver) Connected() bool {
	return d.Status().State == integrations.StateConnected
}

// SubscribeChanges calls fn with every item state change, live and caught
// up, and returns the function that stops it.
func (d *Driver) SubscribeChanges(fn func(ItemChange)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.handlers[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.handlers, id)
		d.watchMu.Unlock()
	}
}

// WatchItems calls fn with the catalog after every connect and every change
// to the item list, and returns the function that stops it.
func (d *Driver) WatchItems(fn func(Catalog)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.itemWatchers[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.itemWatchers, id)
		d.watchMu.Unlock()
	}
}

func (d *Driver) fanOut(c ItemChange) {
	d.watchMu.Lock()
	handlers := make([]func(ItemChange), 0, len(d.handlers))
	for _, h := range d.handlers {
		handlers = append(handlers, h)
	}
	d.watchMu.Unlock()
	for _, h := range handlers {
		h(c)
	}
}

func (d *Driver) notifyItems() {
	catalog := d.Items()
	d.watchMu.Lock()
	watchers := make([]func(Catalog), 0, len(d.itemWatchers))
	for _, w := range d.itemWatchers {
		watchers = append(watchers, w)
	}
	d.watchMu.Unlock()
	for _, w := range watchers {
		w(catalog)
	}
}

// authorize adds the API token when the connection has one.
func (d *Driver) authorize(req *http.Request) {
	if strings.TrimSpace(d.cfg.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(d.cfg.Token))
	}
}

// get reads one REST path.
func (d *Driver) get(ctx context.Context, path string) ([]byte, error) {
	return get(ctx, d.client, d.cfg, path)
}

func get(ctx context.Context, client *http.Client, cfg Config, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.root()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(cfg.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Token))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("reading "+path, resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// readVersion reads the runtime version from the API root.
func (d *Driver) readVersion(ctx context.Context) (string, error) {
	return readVersion(ctx, d.client, d.cfg)
}

func readVersion(ctx context.Context, client *http.Client, cfg Config) (string, error) {
	raw, err := get(ctx, client, cfg, "/rest/")
	if err != nil {
		return "", err
	}
	var root struct {
		RuntimeInfo struct {
			Version string `json:"version"`
		} `json:"runtimeInfo"`
	}
	if json.Unmarshal(raw, &root) != nil || root.RuntimeInfo.Version == "" {
		return "", fmt.Errorf("not found: is this an openHAB server?")
	}
	return root.RuntimeInfo.Version, nil
}

// readItems reads every item with its semantic metadata.
func (d *Driver) readItems(ctx context.Context) (Catalog, error) {
	return readItems(ctx, d.client, d.cfg)
}

func readItems(ctx context.Context, client *http.Client, cfg Config) (Catalog, error) {
	raw, err := get(ctx, client, cfg, "/rest/items?recursive=false&metadata=semantics")
	if err != nil {
		return nil, err
	}
	list, err := decodeItems(raw)
	if err != nil {
		return nil, fmt.Errorf("reading the item list: %w", err)
	}
	out := make(Catalog, len(list))
	for _, it := range list {
		out[it.Name] = it
	}
	return out, nil
}

// SendCommand sends a command to an item or group. openHAB's refusal comes
// back with its reason.
func (d *Driver) SendCommand(ctx context.Context, name, command string) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.cfg.root()+"/rest/items/"+url.PathEscape(name), strings.NewReader(command))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	d.authorize(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending %s to %s: %w", command, name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusError(fmt.Sprintf("sending %s to %s", command, name), resp)
	}
	return nil
}

// statusError words a refused request: authorization, a missing item or
// server, or openHAB's own message.
func statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(body))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: unauthorized: check the API token", what)
	case http.StatusNotFound:
		if msg == "" || bytes.HasPrefix(body, []byte("<")) {
			msg = "not found"
		}
		return fmt.Errorf("%s: %s", what, msg)
	}
	if msg == "" || bytes.HasPrefix(body, []byte("<")) {
		msg = resp.Status
	}
	return fmt.Errorf("%s: openHAB answered %d: %s", what, resp.StatusCode, msg)
}
