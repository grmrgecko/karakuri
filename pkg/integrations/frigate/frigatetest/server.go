// Package frigatetest is a simulated Frigate server for tests: its HTTP API,
// MJPEG streams, event media, and websocket, with hooks to push messages and
// change what it reports.
package frigatetest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Version is the version the server reports.
const Version = "0.18.2"

// Boundary is the MJPEG stream's part boundary.
const Boundary = "frame"

// JPEG is a minimal image the server serves as every frame and snapshot.
var JPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'k', 'a', 'r', 'a', 0xFF, 0xD9}

// Camera is one simulated camera.
type Camera struct {
	Zones   []string
	Objects []string
	FPS     float64
	// Switches are detect, record, snapshots, and motion.
	Switches map[string]bool
}

// Server is a simulated Frigate.
type Server struct {
	*httptest.Server

	// Username and Password require a login when set.
	Username, Password string

	mu        sync.Mutex
	cameras   map[string]*Camera
	tokens    map[string]bool
	events    []map[string]any
	clip      []byte
	sockets   map[*websocket.Conn]context.CancelFunc
	commands  []Command
	streams   int
	openNow   int
	snapshots []string
	logins    int
}

// Command is one set message the server received.
type Command struct {
	Topic   string
	Payload string
}

// New starts a server with two cameras, doorbell and garage, both receiving.
func New() *Server {
	s := &Server{
		cameras: map[string]*Camera{
			"doorbell": {Zones: []string{"driveway", "porch"}, Objects: []string{"person", "car"}, FPS: 5,
				Switches: map[string]bool{"detect": true, "record": true, "snapshots": true, "motion": true}},
			"garage": {Zones: []string{}, Objects: []string{"person"}, FPS: 5,
				Switches: map[string]bool{"detect": true, "record": false, "snapshots": true, "motion": true}},
		},
		tokens:  map[string]bool{},
		sockets: map[*websocket.Conn]context.CancelFunc{},
		clip:    []byte(strings.Repeat("0123456789", 100)),
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// authorized checks the session cookie when the server needs a login.
func (s *Server) authorized(r *http.Request) bool {
	if s.Username == "" {
		return true
	}
	ck, err := r.Cookie("frigate_token")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[ck.Value]
}

// ExpireTokens forgets every session, as Frigate does when a token expires.
func (s *Server) ExpireTokens() {
	s.mu.Lock()
	s.tokens = map[string]bool{}
	s.mu.Unlock()
}

// Logins returns how many logins succeeded.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/login" && r.Method == http.MethodPost {
		var body struct{ User, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.User != s.Username || body.Password != s.Password {
			http.Error(w, `{"message":"Login failed"}`, http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		s.logins++
		token := fmt.Sprintf("token-%d", s.logins)
		s.tokens[token] = true
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "frigate_token", Value: token})
		_, _ = w.Write([]byte(`{}`))
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case path == "/ws":
		s.serveWS(w, r)
	case path == "/api/version":
		_, _ = w.Write([]byte(Version + "-test"))
	case path == "/api/config":
		s.serveConfig(w)
	case path == "/api/stats":
		s.serveStats(w)
	case path == "/api/events":
		s.serveEvents(w, r)
	case strings.HasPrefix(path, "/api/events/"):
		s.serveEventMedia(w, r, strings.TrimPrefix(path, "/api/events/"))
	case strings.HasSuffix(path, "/latest.jpg"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/"), "/latest.jpg")
		if !s.hasCamera(name) {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.snapshots = append(s.snapshots, r.URL.RawQuery)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(JPEG)
	case strings.HasPrefix(path, "/api/") && s.hasCamera(strings.TrimPrefix(path, "/api/")):
		s.serveStream(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) hasCamera(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.cameras[name]
	return ok
}

func (s *Server) serveConfig(w http.ResponseWriter) {
	s.mu.Lock()
	cams := map[string]any{}
	for name, c := range s.cameras {
		zones := map[string]any{}
		for _, z := range c.Zones {
			zones[z] = map[string]any{"coordinates": "0,0,1,1"}
		}
		cams[name] = map[string]any{
			"enabled": true, "zones": zones, "objects": map[string]any{"track": c.Objects},
			"motion":    map[string]any{"enabled": c.Switches["motion"]},
			"detect":    map[string]any{"enabled": c.Switches["detect"]},
			"record":    map[string]any{"enabled": c.Switches["record"]},
			"snapshots": map[string]any{"enabled": c.Switches["snapshots"]},
		}
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"cameras": cams})
}

func (s *Server) serveStats(w http.ResponseWriter) {
	s.mu.Lock()
	cams := map[string]any{}
	for name, c := range s.cameras {
		cams[name] = map[string]any{"camera_fps": c.FPS, "process_fps": c.FPS}
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"cameras": cams, "service": map[string]any{"version": Version}})
}

// AddEvent stores an event the API lists, newest first.
func (s *Server) AddEvent(e map[string]any) {
	s.mu.Lock()
	s.events = append([]map[string]any{e}, s.events...)
	s.mu.Unlock()
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := func(key string) map[string]bool {
		if q.Get(key) == "" {
			return nil
		}
		out := map[string]bool{}
		for _, v := range strings.Split(q.Get(key), ",") {
			out[v] = true
		}
		return out
	}
	cams, labels := filter("cameras"), filter("labels")
	limit, _ := strconv.Atoi(q.Get("limit"))
	s.mu.Lock()
	out := []map[string]any{}
	for _, e := range s.events {
		if cams != nil && !cams[fmt.Sprint(e["camera"])] {
			continue
		}
		if labels != nil && !labels[fmt.Sprint(e["label"])] {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) serveEventMedia(w http.ResponseWriter, r *http.Request, rest string) {
	id, file, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	var event map[string]any
	for _, e := range s.events {
		if e["id"] == id {
			event = e
		}
	}
	clip := s.clip
	s.mu.Unlock()
	if event == nil {
		http.NotFound(w, r)
		return
	}
	switch file {
	case "":
		_ = json.NewEncoder(w).Encode(event)
	case "snapshot.jpg":
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(JPEG)
	case "thumbnail.webp":
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("RIFF....WEBP"))
	case "clip.mp4":
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "clip.mp4", time.Time{}, strings.NewReader(string(clip)))
	default:
		http.NotFound(w, r)
	}
}

// Streams returns how many MJPEG streams were opened and how many are open.
func (s *Server) Streams() (opened, open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams, s.openNow
}

// SnapshotQueries returns the query strings of snapshot requests.
func (s *Server) SnapshotQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.snapshots...)
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.streams++
	s.openNow++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.openNow--
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace;boundary="+Boundary)
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n%s\r\n", Boundary, len(JPEG), JPEG)
		if err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	s.mu.Lock()
	s.sockets[conn] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sockets, conn)
		s.mu.Unlock()
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		var msg struct {
			Topic   string `json:"topic"`
			Payload any    `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}
		if msg.Topic == "onConnect" {
			_ = wsjson.Write(ctx, conn, map[string]any{"topic": "camera_activity", "payload": s.activity()})
			continue
		}
		parts := strings.Split(msg.Topic, "/")
		if len(parts) == 3 && parts[2] == "set" {
			payload := fmt.Sprint(msg.Payload)
			s.mu.Lock()
			s.commands = append(s.commands, Command{Topic: msg.Topic, Payload: payload})
			cam, ok := s.cameras[parts[0]]
			if ok {
				cam.Switches[parts[1]] = payload == "ON"
			}
			s.mu.Unlock()
			if ok {
				_ = wsjson.Write(ctx, conn, map[string]any{"topic": parts[0] + "/" + parts[1] + "/state", "payload": payload})
			}
		}
	}
}

// activity is the camera_activity payload, a JSON string as Frigate sends it.
func (s *Server) activity() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{}
	for name, c := range s.cameras {
		out[name] = map[string]any{"motion": false, "objects": []any{}, "config": map[string]any{
			"detect": c.Switches["detect"], "record": c.Switches["record"], "snapshots": c.Switches["snapshots"],
			"enabled": true, "alerts": true, "detections": true,
		}}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// Commands returns the set messages received.
func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Command{}, s.commands...)
}

// Push sends a message to every connected websocket. payload is encoded to a
// JSON string first, as Frigate does for structured topics.
func (s *Server) Push(topic string, payload any) {
	text, ok := payload.(string)
	if !ok {
		b, _ := json.Marshal(payload)
		text = string(b)
	}
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.sockets))
	for c := range s.sockets {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = wsjson.Write(ctx, c, map[string]any{"topic": topic, "payload": text})
		cancel()
	}
}

// Sockets returns how many websockets are connected.
func (s *Server) Sockets() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sockets)
}

// DropSockets closes every websocket.
func (s *Server) DropSockets() {
	s.mu.Lock()
	for c, cancel := range s.sockets {
		cancel()
		_ = c.CloseNow()
	}
	s.mu.Unlock()
}

// SetFPS changes a camera's reported frame rate.
func (s *Server) SetFPS(name string, fps float64) {
	s.mu.Lock()
	if c, ok := s.cameras[name]; ok {
		c.FPS = fps
	}
	s.mu.Unlock()
}

// AddCamera adds a camera to the config, as an edit of Frigate's config does.
func (s *Server) AddCamera(name string, c Camera) {
	if c.Switches == nil {
		c.Switches = map[string]bool{"detect": true, "record": true, "snapshots": true, "motion": true}
	}
	s.mu.Lock()
	s.cameras[name] = &c
	s.mu.Unlock()
}

// CameraNames returns the camera names.
func (s *Server) CameraNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.cameras))
	for n := range s.cameras {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EventMessage builds an events payload for one tracked object.
func EventMessage(msgType, id, cameraName, label string, score float64, before, after []string) map[string]any {
	obj := func(zones []string) map[string]any {
		o := map[string]any{"id": id, "camera": cameraName, "label": label, "sub_label": nil,
			"top_score": score, "score": score, "start_time": 1793910167.5, "end_time": nil,
			"current_zones": zones, "entered_zones": zones, "has_clip": true, "has_snapshot": true}
		if msgType == "end" {
			o["end_time"] = 1793910200.25
			o["current_zones"] = []string{}
		}
		return o
	}
	return map[string]any{"type": msgType, "before": obj(before), "after": obj(after)}
}
