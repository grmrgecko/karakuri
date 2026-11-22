// Package openhabtest runs a fake openHAB REST API and event stream for tests
// of the openHAB driver: an item table, recorded commands, and a server-sent
// event stream the test pushes events into.
package openhabtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// Command is one recorded command.
type Command struct {
	Item    string
	Command string
}

// Server is a fake openHAB. Create with New; URL is its base URL.
type Server struct {
	URL     string
	Version string
	// Token, when set, is the only bearer token accepted; a request with
	// another token is refused with 401. Requests with none are anonymous,
	// as openHAB allows by default.
	Token string

	hs *httptest.Server

	mu       sync.Mutex
	items    map[string]map[string]any
	commands []Command
	refuse   map[string]bool
	streams  map[chan string]struct{}
	// Autoupdate applies a command to the item's state and announces the
	// change, as openHAB's autoupdate does for items no binding confirms.
	Autoupdate bool
}

// New starts a fake openHAB.
func New() *Server {
	s := &Server{Version: "5.2.3", items: map[string]map[string]any{}, refuse: map[string]bool{},
		streams: map[chan string]struct{}{}, Autoupdate: true}
	s.hs = httptest.NewServer(s)
	s.URL = s.hs.URL
	return s
}

// Close stops the server.
func (s *Server) Close() {
	s.Drop()
	s.hs.Close()
}

// SetItem adds or replaces an item as the REST API lists it: name, label,
// type, state, and optionally groupNames, tags, stateDescription, groupType,
// and metadata.semantics.
func (s *Server) SetItem(item map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := item["state"]; !ok {
		item["state"] = "NULL"
	}
	s.items[item["name"].(string)] = item
}

// RemoveItem removes an item without announcing it.
func (s *Server) RemoveItem(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
}

// State returns an item's state.
func (s *Server) State(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[name]; ok {
		return fmt.Sprint(it["state"])
	}
	return ""
}

// SetState changes an item's state and announces it on the stream.
func (s *Server) SetState(name, state string) {
	s.mu.Lock()
	it, ok := s.items[name]
	old := "NULL"
	if ok {
		old = fmt.Sprint(it["state"])
		it["state"] = state
	}
	s.mu.Unlock()
	if ok && old != state {
		s.Push("openhab/items/"+name+"/statechanged", "ItemStateChangedEvent",
			map[string]any{"type": "String", "value": state, "oldType": "String", "oldValue": old})
	}
}

// SetStateQuietly changes an item's state without announcing it, as a change
// made while the stream was down.
func (s *Server) SetStateQuietly(name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[name]; ok {
		it["state"] = state
	}
}

// RefuseCommands makes commands to an item fail with 400.
func (s *Server) RefuseCommands(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse[name] = true
}

// Commands returns the commands received so far.
func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Command(nil), s.commands...)
}

// Streams reports how many event streams are open.
func (s *Server) Streams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// Push sends one event to every open stream; payload is encoded to the JSON
// string openHAB sends.
func (s *Server) Push(topic, typ string, payload any) {
	raw, _ := json.Marshal(payload)
	data, _ := json.Marshal(map[string]any{"topic": topic, "payload": string(raw), "type": typ})
	s.send("event: message\ndata: " + string(data) + "\n\n")
}

// Alive sends the keepalive event.
func (s *Server) Alive() {
	s.send("event: alive\ndata: {\"type\":\"ALIVE\",\"interval\":10}\n\n")
}

// Announce sends an added, removed, or updated event for an item.
func (s *Server) Announce(name, kind string) {
	s.mu.Lock()
	it := s.items[name]
	s.mu.Unlock()
	if it == nil {
		it = map[string]any{"name": name, "type": "Switch"}
	}
	s.Push("openhab/items/"+name+"/"+kind, "Item"+strings.ToUpper(kind[:1])+kind[1:]+"Event", it)
}

// Drop closes every open event stream.
func (s *Server) Drop() {
	s.mu.Lock()
	for ch := range s.streams {
		close(ch)
		delete(s.streams, ch)
	}
	s.mu.Unlock()
}

func (s *Server) send(frame string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.streams {
		select {
		case ch <- frame:
		default:
		}
	}
}

// ServeHTTP answers the REST paths the driver uses.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); auth != "" && s.Token != "" && auth != "Bearer "+s.Token {
		http.Error(w, `{"error":{"message":"Authentication required","http-code":401}}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/rest/":
		writeJSON(w, map[string]any{"version": "8", "runtimeInfo": map[string]any{"version": s.Version}})
	case r.URL.Path == "/rest/events":
		s.serveEvents(w, r)
	case r.URL.Path == "/rest/items" && r.Method == http.MethodGet:
		s.mu.Lock()
		names := make([]string, 0, len(s.items))
		for name := range s.items {
			names = append(names, name)
		}
		sort.Strings(names)
		list := make([]map[string]any, 0, len(names))
		for _, name := range names {
			list = append(list, s.items[name])
		}
		s.mu.Unlock()
		writeJSON(w, list)
	case strings.HasPrefix(r.URL.Path, "/rest/items/"):
		s.serveItem(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/rest/items/")
	name, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	it, ok := s.items[name]
	refused := s.refuse[name]
	s.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":{"message":"Item `+name+` does not exist!","http-code":404}}`, http.StatusNotFound)
		return
	}
	switch {
	case r.Method == http.MethodGet && sub == "state":
		_, _ = io.WriteString(w, fmt.Sprint(it["state"]))
	case r.Method == http.MethodGet:
		writeJSON(w, it)
	case r.Method == http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		cmd := string(body)
		if refused {
			http.Error(w, `{"error":{"message":"Cannot parse command `+cmd+`","http-code":400}}`, http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.commands = append(s.commands, Command{Item: name, Command: cmd})
		auto := s.Autoupdate
		s.mu.Unlock()
		if auto {
			s.SetState(name, cmd)
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ch := make(chan string, 256)
	s.mu.Lock()
	s.streams[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if _, ok := s.streams[ch]; ok {
			delete(s.streams, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, frame); err != nil {
				return
			}
			flusher.Flush()
		case <-time.After(time.Hour):
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// House fills the fake with the items of test/openhab/conf/items/karakuri.items:
// a ground floor with a living room and a kitchen, a lamp in the living room,
// and one item of each mapped type.
func (s *Server) House() {
	sem := func(value string, config map[string]any) map[string]any {
		m := map[string]any{"value": value}
		if config != nil {
			m["config"] = config
		}
		return map[string]any{"semantics": m}
	}
	items := []map[string]any{
		{"name": "gGround", "label": "Ground floor", "type": "Group", "tags": []string{"GroundFloor"},
			"metadata": sem("Location_Indoor_Floor_GroundFloor", nil)},
		{"name": "gLiving", "label": "Living room", "type": "Group", "groupNames": []string{"gGround"},
			"metadata": sem("Location_Indoor_Room_LivingRoom", map[string]any{"isPartOf": "gGround"})},
		{"name": "gKitchen", "label": "Kitchen", "type": "Group", "groupNames": []string{"gGround"},
			"metadata": sem("Location_Indoor_Room_Kitchen", map[string]any{"isPartOf": "gGround"})},
		{"name": "gLamp", "label": "Lamp", "type": "Group", "groupNames": []string{"gLiving"},
			"metadata": sem("Equipment_LightSource_Lightbulb", map[string]any{"hasLocation": "gLiving"})},
		{"name": "gLights", "label": "All lights", "type": "Group", "groupType": "Switch", "state": "OFF"},
		{"name": "LivingDimmer", "label": "Living dimmer", "type": "Dimmer", "state": "0",
			"groupNames": []string{"gLamp", "gLights"},
			"metadata":   sem("Point_Control", map[string]any{"isPointOf": "gLamp"})},
		{"name": "LivingColor", "label": "Living colour", "type": "Color", "state": "0,0,0",
			"groupNames": []string{"gLiving"},
			"metadata":   sem("Point_Control", map[string]any{"hasLocation": "gLiving"})},
		{"name": "KitchenSwitch", "label": "Kitchen switch", "type": "Switch", "state": "OFF",
			"groupNames": []string{"gKitchen", "gLights"},
			"metadata":   sem("Point_Control_Switch", map[string]any{"hasLocation": "gKitchen"})},
		{"name": "KitchenBlind", "label": "Kitchen blind", "type": "Rollershutter", "state": "0",
			"groupNames": []string{"gKitchen"},
			"metadata":   sem("Point_Control", map[string]any{"hasLocation": "gKitchen"})},
		{"name": "KitchenTemp", "label": "Kitchen temperature", "type": "Number:Temperature", "state": "21.5 °C",
			"groupNames": []string{"gKitchen"}, "stateDescription": map[string]any{"pattern": "%.1f %unit%", "readOnly": false},
			"metadata": sem("Point_Measurement", map[string]any{"hasLocation": "gKitchen"})},
		{"name": "Volume", "label": "Volume", "type": "Number", "state": "10", "groupNames": []string{"gLiving"},
			"stateDescription": map[string]any{"minimum": 0, "maximum": 50, "step": 5, "pattern": "%d", "readOnly": false}},
		{"name": "Mode", "label": "Mode", "type": "String", "state": "day", "groupNames": []string{"gLiving"},
			"stateDescription": map[string]any{"pattern": "%s", "readOnly": false, "options": []map[string]any{
				{"value": "day", "label": "Day"}, {"value": "night", "label": "Night"}}}},
		{"name": "FrontDoor", "label": "Front door", "type": "Contact", "state": "CLOSED", "groupNames": []string{"gGround"},
			"metadata": sem("Point_Control", map[string]any{"hasLocation": "gGround"})},
		{"name": "LastSeen", "label": "Last seen", "type": "DateTime", "state": "NULL"},
		{"name": "Speaker", "label": "Speaker", "type": "Player", "state": "PAUSE", "groupNames": []string{"gLiving"}},
		{"name": "Phone", "label": "Phone location", "type": "Location", "state": "41.88,-87.63,180"},
		{"name": "NoLocation", "label": "Loose switch", "type": "Switch", "state": "OFF"},
		{"name": "Snapshot", "label": "Snapshot", "type": "Image", "state": "NULL"},
	}
	for _, it := range items {
		s.SetItem(it)
	}
}
