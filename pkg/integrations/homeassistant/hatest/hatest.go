// Package hatest provides a fake Home Assistant WebSocket server for tests
// and E2E helpers: it implements the auth handshake, id-correlated command
// results, event subscriptions with pushed events, scriptable call_service
// outcomes, and get_states backed by an in-memory entity table.
package hatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ServiceCall is one recorded call_service command.
type ServiceCall struct {
	Domain      string         `json:"domain"`
	Service     string         `json:"service"`
	ServiceData map[string]any `json:"service_data,omitempty"`
	Target      map[string]any `json:"target,omitempty"`
}

// failure scripts call_service errors for one domain.service pair.
type failure struct {
	remaining int
	message   string
}

// clientConn is one authenticated WebSocket client and its subscriptions.
type clientConn struct {
	ws     *websocket.Conn
	cancel context.CancelFunc

	mu   sync.Mutex
	subs map[int64]string // subscription id -> event type ("" = all)
}

// write sends one message on the client socket, serialized per connection.
func (c *clientConn) write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c.ws, v)
}

// Server is a fake Home Assistant instance. Create with New (self-hosted on
// an httptest server) or NewDetached (mount ServeHTTP yourself).
type Server struct {
	Token string
	URL   string

	hs *httptest.Server

	mu             sync.Mutex
	services       map[string]map[string]any
	states         map[string]map[string]any
	stateOrder     []string
	calls          []ServiceCall
	failures       map[string]*failure
	delays         map[string]time.Duration
	dropPings      bool
	stateReadError string
	conns          map[*clientConn]struct{}
	subCounts      map[string]int
	connCount      int
	todos          map[string][]map[string]any
	history        map[string][]map[string]any
	statistics     map[string][]map[string]any
	images         map[string][]byte
	registries     map[string]any
	unknown        map[string]bool
}

// Registry list commands the fake answers, by the registry name SetRegistry
// and PushRegistryUpdate take.
var registryCommands = map[string]string{
	"floor":  "config/floor_registry/list",
	"area":   "config/area_registry/list",
	"device": "config/device_registry/list",
	"entity": "config/entity_registry/list_for_display",
	"label":  "config/label_registry/list",
}

// NewDetached creates a fake server without an HTTP listener; mount its
// ServeHTTP on your own server (the WebSocket endpoint is /api/websocket).
func NewDetached(token string) *Server {
	return &Server{
		Token:      token,
		states:     make(map[string]map[string]any),
		failures:   make(map[string]*failure),
		delays:     make(map[string]time.Duration),
		conns:      make(map[*clientConn]struct{}),
		subCounts:  make(map[string]int),
		todos:      make(map[string][]map[string]any),
		history:    make(map[string][]map[string]any),
		statistics: make(map[string][]map[string]any),
		images:     make(map[string][]byte),
		registries: make(map[string]any),
		unknown:    make(map[string]bool),
	}
}

// SetRegistry sets what one registry's list command returns: "floor",
// "area", "device", and "label" take a list of records as Home Assistant
// writes them; "entity" takes the list_for_display entries (short keys ei,
// di, ai, lb, en, hb, ec). A registry never set answers empty.
func (s *Server) SetRegistry(name string, records []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registries[name] = records
}

// UnknownCommand makes a command answer unknown_command, as a Home Assistant
// older than the command does.
func (s *Server) UnknownCommand(command string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unknown[command] = true
}

// PushRegistryUpdate sends the <name>_registry_updated event Home Assistant
// fires after a registry change. data carries the action and IDs, such as
// {"action": "update", "entity_id": "light.a", "old_entity_id": "light.b"}.
func (s *Server) PushRegistryUpdate(name string, data map[string]any) {
	eventType := name + "_registry_updated"
	s.PushEvent(eventType, map[string]any{
		"event_type": eventType,
		"data":       data,
		"origin":     "LOCAL",
		"time_fired": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// registryResult builds one registry list reply.
func (s *Server) registryResult(name string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, _ := s.registries[name].([]map[string]any)
	if records == nil {
		records = []map[string]any{}
	}
	if name == "entity" {
		return map[string]any{
			"entity_categories": map[string]string{"0": "config", "1": "diagnostic"},
			"entities":          records,
		}
	}
	return records
}

// SetTodoItems sets the items todo/item/list returns for an entity.
func (s *Server) SetTodoItems(entityID string, items []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.todos[entityID] = items
}

// SetHistory sets the compressed minimal history entries
// history/history_during_period returns for an entity, as Home Assistant
// writes them: {"s": state, "lu": fractional Unix seconds}.
func (s *Server) SetHistory(entityID string, entries []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[entityID] = entries
}

// SetStatistics sets the rows recorder/statistics_during_period returns for a
// statistic ID, as Home Assistant writes them: {"start": Unix ms, "mean": ...}.
func (s *Server) SetStatistics(statisticID string, rows []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statistics[statisticID] = rows
}

// SetImage serves body at an /api/ path, such as /api/camera_proxy/camera.door,
// to requests carrying the server's bearer token.
func (s *Server) SetImage(path string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images[path] = body
}

// SetServices replaces the service catalog returned by get_services.
func (s *Server) SetServices(services map[string]map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = services
}

// New creates a fake server listening on an httptest server. Its URL field is
// the base URL to use in connection configs.
func New(token string) *Server {
	s := NewDetached(token)
	s.hs = httptest.NewServer(s)
	s.URL = s.hs.URL
	return s
}

// Close shuts the server down and disconnects every client.
func (s *Server) Close() {
	s.CloseClients()
	if s.hs != nil {
		s.hs.Close()
	}
}

// SetState stores an entity state object served by get_states.
func (s *Server) SetState(entityID, state string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[entityID]; !ok {
		s.stateOrder = append(s.stateOrder, entityID)
	}
	s.states[entityID] = StateObj(entityID, state, attrs)
}

// RemoveState drops an entity from get_states, as Home Assistant does when an
// integration removes it.
func (s *Server) RemoveState(entityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, entityID)
	for i, id := range s.stateOrder {
		if id == entityID {
			s.stateOrder = append(s.stateOrder[:i], s.stateOrder[i+1:]...)
			break
		}
	}
}

// StateObj builds a Home Assistant state object.
func StateObj(entityID, state string, attrs map[string]any) map[string]any {
	if attrs == nil {
		attrs = map[string]any{}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"entity_id":    entityID,
		"state":        state,
		"attributes":   attrs,
		"last_changed": now,
		"last_updated": now,
	}
}

// FailStateReads makes get_states return the supplied protocol error.
func (s *Server) FailStateReads(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateReadError = message
}

// State returns the stored state object for an entity, or nil.
func (s *Server) State(entityID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[entityID]
}

// FailService scripts the next n call_service commands for domain.service to
// fail with the given message.
func (s *Server) FailService(domain, service string, times int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[domain+"."+service] = &failure{remaining: times, message: message}
}

// SlowService holds the reply to call_service commands for domain.service for
// the given delay, standing in for a service handler that drives real devices
// before Home Assistant answers.
func (s *Server) SlowService(domain, service string, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays[domain+"."+service] = delay
}

// SlowSubscribe holds the reply to subscribe_events commands for an event
// type for the given delay, standing in for a Home Assistant that is still
// starting and answers late.
func (s *Server) SlowSubscribe(eventType string, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays["subscribe_events:"+eventType] = delay
}

// ServiceCalls returns a snapshot of all recorded call_service commands.
func (s *Server) ServiceCalls() []ServiceCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServiceCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// SubscribeCount returns how many subscribe_events commands were received for
// an event type across all connections.
func (s *Server) SubscribeCount(eventType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subCounts[eventType]
}

// ConnCount returns how many WebSocket clients have authenticated.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connCount
}

// SetDropPings makes the server ignore ping commands, simulating a stalled
// instance so clients hit their ping timeout.
func (s *Server) SetDropPings(drop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropPings = drop
}

// CloseClients force-closes every connected client, simulating a server-side
// disconnect.
func (s *Server) CloseClients() {
	s.mu.Lock()
	conns := make([]*clientConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.Close(websocket.StatusGoingAway, "server closing")
		c.cancel()
	}
}

// PushEvent delivers an event to every client subscription matching the event
// type.
func (s *Server) PushEvent(eventType string, event map[string]any) {
	s.mu.Lock()
	type target struct {
		conn  *clientConn
		subID int64
	}
	var targets []target
	for c := range s.conns {
		c.mu.Lock()
		for id, et := range c.subs {
			if et == "" || et == eventType {
				targets = append(targets, target{conn: c, subID: id})
			}
		}
		c.mu.Unlock()
	}
	s.mu.Unlock()

	for _, t := range targets {
		_ = t.conn.write(map[string]any{"id": t.subID, "type": "event", "event": event})
	}
}

// PushStateChanged updates the stored state and delivers a state_changed
// event built from the given old and new state objects (either may be nil).
func (s *Server) PushStateChanged(entityID string, oldState, newState map[string]any) {
	s.mu.Lock()
	if newState != nil {
		if _, ok := s.states[entityID]; !ok {
			s.stateOrder = append(s.stateOrder, entityID)
		}
		s.states[entityID] = newState
	} else {
		delete(s.states, entityID)
	}
	s.mu.Unlock()

	s.PushEvent("state_changed", map[string]any{
		"event_type": "state_changed",
		"data": map[string]any{
			"entity_id": entityID,
			"old_state": oldState,
			"new_state": newState,
		},
		"origin":     "LOCAL",
		"time_fired": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ServeHTTP serves the WebSocket API endpoint at /api/websocket.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/websocket" {
		s.mu.Lock()
		body, ok := s.images[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	s.serveClient(r.Context(), ws)
}

// serveClient runs the handshake and command loop for one client.
func (s *Server) serveClient(ctx context.Context, ws *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	client := &clientConn{ws: ws, cancel: cancel, subs: make(map[int64]string)}
	defer func() {
		s.mu.Lock()
		delete(s.conns, client)
		s.mu.Unlock()
		cancel()
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}()

	// Auth handshake.
	if err := client.write(map[string]any{"type": "auth_required", "ha_version": "2025.1.0"}); err != nil {
		return
	}
	var auth struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
	}
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	err := wsjson.Read(readCtx, ws, &auth)
	readCancel()
	if err != nil || auth.Type != "auth" || auth.AccessToken != s.Token {
		_ = client.write(map[string]any{"type": "auth_invalid", "message": "Invalid access token"})
		return
	}
	if err := client.write(map[string]any{"type": "auth_ok", "ha_version": "2025.1.0"}); err != nil {
		return
	}

	s.mu.Lock()
	s.conns[client] = struct{}{}
	s.connCount++
	s.mu.Unlock()

	// Command loop.
	for {
		var cmd map[string]any
		if err := wsjson.Read(ctx, ws, &cmd); err != nil {
			return
		}
		s.handleCommand(client, cmd)
	}
}

// handleCommand processes one client command and writes its reply.
func (s *Server) handleCommand(client *clientConn, cmd map[string]any) {
	id := int64(0)
	if v, ok := cmd["id"].(float64); ok {
		id = int64(v)
	}
	cmdType, _ := cmd["type"].(string)

	s.mu.Lock()
	unknown := s.unknown[cmdType]
	s.mu.Unlock()
	if unknown {
		s.result(client, id, false, nil, map[string]any{"code": "unknown_command", "message": "Unknown command."})
		return
	}
	for name, command := range registryCommands {
		if command == cmdType {
			s.result(client, id, true, s.registryResult(name), nil)
			return
		}
	}

	switch cmdType {
	case "ping":
		s.mu.Lock()
		drop := s.dropPings
		s.mu.Unlock()
		if !drop {
			_ = client.write(map[string]any{"id": id, "type": "pong"})
		}

	case "subscribe_events":
		eventType, _ := cmd["event_type"].(string)
		s.mu.Lock()
		delay := s.delays["subscribe_events:"+eventType]
		s.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		client.mu.Lock()
		client.subs[id] = eventType
		client.mu.Unlock()
		s.mu.Lock()
		s.subCounts[eventType]++
		s.mu.Unlock()
		s.result(client, id, true, nil, nil)

	case "unsubscribe_events":
		if v, ok := cmd["subscription"].(float64); ok {
			client.mu.Lock()
			delete(client.subs, int64(v))
			client.mu.Unlock()
		}
		s.result(client, id, true, nil, nil)

	case "get_services":
		s.mu.Lock()
		services := s.services
		s.mu.Unlock()
		if services == nil {
			services = map[string]map[string]any{}
		}
		s.result(client, id, true, services, nil)

	case "call_service":
		domain, _ := cmd["domain"].(string)
		service, _ := cmd["service"].(string)
		call := ServiceCall{Domain: domain, Service: service}
		if sd, ok := cmd["service_data"].(map[string]any); ok {
			call.ServiceData = sd
		}
		if tg, ok := cmd["target"].(map[string]any); ok {
			call.Target = tg
		}
		s.mu.Lock()
		s.calls = append(s.calls, call)
		f := s.failures[domain+"."+service]
		fail := f != nil && f.remaining > 0
		var failMsg string
		if fail {
			f.remaining--
			failMsg = f.message
		}
		delay := s.delays[domain+"."+service]
		s.mu.Unlock()

		// A scripted delay replies off the read loop, the way Home Assistant
		// keeps serving commands while a service handler is still running.
		reply := func() {
			if fail {
				s.result(client, id, false, nil, map[string]any{"code": "unknown_error", "message": failMsg})
				return
			}
			s.result(client, id, true, map[string]any{"context": map[string]any{"id": fmt.Sprintf("ctx-%d", id)}}, nil)
		}
		if delay > 0 {
			go func() {
				time.Sleep(delay)
				reply()
			}()
			return
		}
		reply()

	case "get_states":
		s.mu.Lock()
		if message := s.stateReadError; message != "" {
			s.mu.Unlock()
			s.result(client, id, false, nil, map[string]any{"code": "unknown_error", "message": message})
			return
		}
		states := make([]map[string]any, 0, len(s.stateOrder))
		for _, eid := range s.stateOrder {
			if st, ok := s.states[eid]; ok {
				states = append(states, st)
			}
		}
		s.mu.Unlock()
		s.result(client, id, true, states, nil)

	case "todo/item/list":
		entityID, _ := cmd["entity_id"].(string)
		s.mu.Lock()
		items, ok := s.todos[entityID]
		s.mu.Unlock()
		if !ok {
			s.result(client, id, false, nil, map[string]any{"code": "not_found", "message": "Entity not found"})
			return
		}
		s.result(client, id, true, map[string]any{"items": items}, nil)

	case "history/history_during_period":
		out := map[string]any{}
		ids, _ := cmd["entity_ids"].([]any)
		s.mu.Lock()
		for _, raw := range ids {
			if eid, ok := raw.(string); ok {
				if entries, ok := s.history[eid]; ok {
					out[eid] = entries
				}
			}
		}
		s.mu.Unlock()
		s.result(client, id, true, out, nil)

	case "recorder/statistics_during_period":
		out := map[string]any{}
		ids, _ := cmd["statistic_ids"].([]any)
		s.mu.Lock()
		for _, raw := range ids {
			if sid, ok := raw.(string); ok {
				if rows, ok := s.statistics[sid]; ok {
					out[sid] = rows
				}
			}
		}
		s.mu.Unlock()
		s.result(client, id, true, out, nil)

	case "recorder/list_statistic_ids":
		s.mu.Lock()
		list := make([]map[string]any, 0, len(s.statistics))
		for sid := range s.statistics {
			list = append(list, map[string]any{"statistic_id": sid, "name": sid, "statistics_unit_of_measurement": "kWh"})
		}
		s.mu.Unlock()
		s.result(client, id, true, list, nil)

	default:
		s.result(client, id, false, nil, map[string]any{"code": "unknown_command", "message": "Unknown command."})
	}
}

// result writes one command result envelope.
func (s *Server) result(client *clientConn, id int64, success bool, result any, errObj map[string]any) {
	msg := map[string]any{"id": id, "type": "result", "success": success}
	if result != nil {
		raw, _ := json.Marshal(result)
		msg["result"] = json.RawMessage(raw)
	} else {
		msg["result"] = nil
	}
	if errObj != nil {
		msg["error"] = errObj
	}
	_ = client.write(msg)
}
