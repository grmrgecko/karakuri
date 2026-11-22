package frigate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate/frigatetest"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configFor is a connection config pointing at a simulated server.
func configFor(t *testing.T, s *frigatetest.Server) []byte {
	t.Helper()
	raw, err := json.Marshal(Config{URL: s.URL, Username: s.Username, Password: s.Password})
	require.NoError(t, err)
	return raw
}

// startDriver connects a driver to the simulated server and waits until it
// is connected.
func startDriver(t *testing.T, s *frigatetest.Server) *Driver {
	t.Helper()
	built, err := New(integrations.ConnModel{ID: uuid.New(), Name: "nvr", Kind: Kind, Config: configFor(t, s)}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateConnected }, 5*time.Second, 10*time.Millisecond)
	return d
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
		sent     string
		want     string
		wantErr  string
	}{
		{name: "no login", want: "Frigate 0.18.2, 2 cameras: doorbell, garage"},
		{name: "login", user: "admin", password: "secret", sent: "secret", want: "Frigate 0.18.2, 2 cameras: doorbell, garage"},
		{name: "wrong password", user: "admin", password: "secret", sent: "wrong", wantErr: "login refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := frigatetest.New()
			defer s.Close()
			s.Username, s.Password = tt.user, tt.password
			raw, _ := json.Marshal(Config{URL: s.URL, Username: tt.user, Password: tt.sent})
			detail, err := Probe(context.Background(), uuid.New(), raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, detail)
		})
	}
}

func TestProbeNeedsLogin(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	s.Username, s.Password = "admin", "secret"
	raw, _ := json.Marshal(Config{URL: s.URL})
	_, err := Probe(context.Background(), uuid.New(), raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a login")
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "ok", cfg: Config{URL: "http://frigate.local:5000"}},
		{name: "no url", cfg: Config{}, wantErr: "requires url"},
		{name: "path", cfg: Config{URL: "http://frigate.local:5000/api"}, wantErr: "no path"},
		{name: "scheme", cfg: Config{URL: "rtsp://frigate.local"}, wantErr: "http or https"},
		{name: "half credentials", cfg: Config{URL: "http://f", Username: "admin"}, wantErr: "both username and password"},
		{name: "bad ca", cfg: Config{URL: "https://f", CACert: "nope"}, wantErr: "no PEM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDriverRelogsAfterExpiredToken(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	s.Username, s.Password = "admin", "secret"
	d := startDriver(t, s)
	before := s.Logins()
	s.ExpireTokens()

	resp, err := d.Snapshot(context.Background(), "doorbell", nvr.ImageOptions{Height: 180})
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, frigatetest.JPEG, body)
	assert.Equal(t, before+1, s.Logins())
	assert.Equal(t, "height=180", s.SnapshotQueries()[len(s.SnapshotQueries())-1])
}

func TestDriverCamerasAndSwitches(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)

	cams := d.Cameras()
	require.Len(t, cams, 2)
	assert.Equal(t, "doorbell", cams[0].Name)
	assert.Equal(t, []string{"driveway", "porch"}, cams[0].Zones)
	assert.Equal(t, []string{"person", "car"}, cams[0].Objects)
	assert.True(t, cams[0].Receiving)
	assert.False(t, cams[1].Switches["record"])
}

func TestDriverEvents(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)

	got := make(chan nvr.Event, 8)
	stop := d.WatchEvents(func(e nvr.Event) { got <- e })
	defer stop()
	require.Eventually(t, func() bool { return s.Sockets() == 1 }, time.Second, 10*time.Millisecond)

	id := "1793910167.5-abc123"
	s.Push("events", frigatetest.EventMessage("new", id, "doorbell", "person", 0.86, nil, []string{}))
	s.Push("events", frigatetest.EventMessage("update", id, "doorbell", "person", 0.9, []string{}, []string{"driveway"}))
	s.Push("events", frigatetest.EventMessage("update", id, "doorbell", "person", 0.9, []string{"driveway"}, []string{"driveway"}))
	s.Push("events", frigatetest.EventMessage("end", id, "doorbell", "person", 0.9, []string{"driveway"}, []string{"driveway"}))

	start := <-got
	assert.Equal(t, nvr.StageStart, start.Stage)
	assert.Equal(t, 0.86, start.Score)
	assert.Equal(t, "/api/connections/"+d.ID().String()+"/nvr/events/"+id+"/snapshot", start.Snapshot)
	entered := <-got
	assert.Equal(t, nvr.StageUpdate, entered.Stage)
	assert.Equal(t, []string{"driveway"}, entered.EnteredZones)
	stayed := <-got
	assert.Empty(t, stayed.EnteredZones)
	end := <-got
	assert.Equal(t, nvr.StageEnd, end.Stage)
	assert.Equal(t, []string{"driveway"}, end.Zones)
	require.NotNil(t, end.EndTime)
}

func TestDriverReviews(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)
	got := make(chan nvr.Review, 1)
	defer d.WatchReviews(func(r nvr.Review) { got <- r })()
	require.Eventually(t, func() bool { return s.Sockets() == 1 }, time.Second, 10*time.Millisecond)

	item := map[string]any{"id": "1793911471.77406-bumb9z", "camera": "garage", "start_time": 1793911471.77406,
		"end_time": nil, "severity": "alert", "data": map[string]any{"objects": []string{"person"}, "zones": []string{}}}
	s.Push("reviews", map[string]any{"type": "new", "before": item, "after": item})
	r := <-got
	assert.Equal(t, "alert", r.Severity)
	assert.Equal(t, nvr.StageStart, r.Stage)
	assert.Equal(t, []string{"person"}, r.Objects)
}

func TestDriverStatsAndConfigChange(t *testing.T) {
	defer SetTimingForTest(20*time.Millisecond, time.Second)()
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)

	var mu sync.Mutex
	var flips []bool
	stop, err := d.WatchEntityAvailability("garage/detect", func(on bool) {
		mu.Lock()
		flips = append(flips, on)
		mu.Unlock()
	})
	require.NoError(t, err)
	defer stop()

	s.SetFPS("garage", 0)
	require.Eventually(t, func() bool { return !d.Receiving("garage") }, time.Second, 10*time.Millisecond)
	_, err = d.Snapshot(context.Background(), "garage", nvr.ImageOptions{})
	var notReceiving *nvr.NotReceivingError
	require.ErrorAs(t, err, &notReceiving)
	assert.Equal(t, `camera "garage" is not receiving frames`, err.Error())

	s.SetFPS("garage", 5)
	require.Eventually(t, func() bool { return d.Receiving("garage") }, time.Second, 10*time.Millisecond)
	mu.Lock()
	assert.Equal(t, []bool{true, false, true}, flips)
	mu.Unlock()

	s.AddCamera("yard", frigatetest.Camera{FPS: 5, Objects: []string{"dog"}})
	require.Eventually(t, func() bool { return len(d.Cameras()) == 3 }, time.Second, 10*time.Millisecond)
}

func TestDriverReconnects(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)
	require.Eventually(t, func() bool { return s.Sockets() == 1 }, time.Second, 10*time.Millisecond)
	s.DropSockets()
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateError }, 2*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateConnected && s.Sockets() == 1 },
		5*time.Second, 10*time.Millisecond)
}

func TestDriverSwitchCommand(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)

	got := make(chan bool, 4)
	stop, err := d.WatchEntity("garage/record", func(v map[string]any) { got <- v["on"].(bool) })
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { return s.Sockets() == 1 }, time.Second, 10*time.Millisecond)
	assert.False(t, <-got)

	require.NoError(t, d.CommandEntity(context.Background(), "garage/record", "on", true))
	select {
	case on := <-got:
		assert.True(t, on)
	case <-time.After(2 * time.Second):
		t.Fatal("no state after the command")
	}
	assert.Equal(t, []frigatetest.Command{{Topic: "garage/record/set", Payload: "ON"}}, s.Commands())

	assert.Error(t, d.CommandEntity(context.Background(), "garage/zoom", "on", true))
}

// TestDriverAnnouncesSwitchesOnConnect locks that a watcher registered before
// the connection is up learns every switch once it is, the motion switch
// included. Frigate's camera_activity message carries detect, record, and
// snapshots but not motion, so a motion switch device added before the first
// connect, as every device is at boot, held no value until someone toggled it.
func TestDriverAnnouncesSwitchesOnConnect(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	built, err := New(integrations.ConnModel{ID: uuid.New(), Name: "nvr", Kind: Kind, Config: configFor(t, s)}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*Driver)

	tests := []struct {
		objectID string
		want     bool
	}{
		{objectID: "doorbell/motion", want: true},
		{objectID: "garage/record", want: false},
	}
	got := make([]chan bool, len(tests))
	for i, tt := range tests {
		ch := make(chan bool, 8)
		got[i] = ch
		stop, err := d.WatchEntity(tt.objectID, func(v map[string]any) { ch <- v["on"].(bool) })
		require.NoError(t, err)
		defer stop()
	}

	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	for i, tt := range tests {
		select {
		case on := <-got[i]:
			assert.Equal(t, tt.want, on, "%s must report the value Frigate holds", tt.objectID)
		case <-time.After(3 * time.Second):
			t.Errorf("%s was never reported after the connect", tt.objectID)
		}
	}
}

func TestDriverEventsAndMedia(t *testing.T) {
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)
	id := "1793910167.5-abc123"
	s.AddEvent(map[string]any{"id": id, "camera": "doorbell", "label": "person", "sub_label": "Alex",
		"zones": []string{"porch"}, "start_time": 1793910167.5, "end_time": 1793910200.25,
		"has_clip": true, "has_snapshot": true, "data": map[string]any{"score": 0.8, "top_score": 0.91}})
	s.AddEvent(map[string]any{"id": "1793910100.1-zzz", "camera": "garage", "label": "person",
		"zones": []string{}, "start_time": 1793910100.1, "data": map[string]any{"score": 0.7}})

	events, err := d.Events(context.Background(), nvr.EventQuery{Cameras: []string{"doorbell"}, Limit: 10})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, 0.91, events[0].Score)
	assert.Equal(t, "Alex", *events[0].SubLabel)
	assert.Equal(t, []string{"porch"}, events[0].Zones)

	e, err := d.Event(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "doorbell", e.Camera)

	resp, err := d.EventMedia(context.Background(), id, nvr.MediaClip, "bytes=10-19")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "0123456789", string(body))

	_, err = d.EventMedia(context.Background(), "../config", nvr.MediaClip, "")
	assert.Error(t, err)
	_, err = d.EventMedia(context.Background(), "1793910167.5-nothere", nvr.MediaSnapshot, "")
	var missing *MediaNotFoundError
	assert.ErrorAs(t, err, &missing)
}

func TestSharedStream(t *testing.T) {
	defer SetStreamLingerForTest(50 * time.Millisecond)()
	s := frigatetest.New()
	defer s.Close()
	d := startDriver(t, s)

	a, err := d.Stream(context.Background(), "doorbell", nvr.ImageOptions{Height: 360})
	require.NoError(t, err)
	b, err := d.Stream(context.Background(), "doorbell", nvr.ImageOptions{Height: 360})
	require.NoError(t, err)
	for _, sub := range []*nvr.Subscription{a, b} {
		select {
		case frame := <-sub.Frames:
			assert.Equal(t, frigatetest.JPEG, frame)
		case <-time.After(2 * time.Second):
			t.Fatal("no frame")
		}
	}
	opened, open := s.Streams()
	assert.Equal(t, 1, opened)
	assert.Equal(t, 1, open)

	a.Close()
	b.Close()
	require.Eventually(t, func() bool { _, open := s.Streams(); return open == 0 }, 2*time.Second, 10*time.Millisecond)

	c, err := d.Stream(context.Background(), "doorbell", nvr.ImageOptions{Height: 360})
	require.NoError(t, err)
	defer c.Close()
	opened, _ = s.Streams()
	assert.Equal(t, 2, opened)
}

func TestReadFrames(t *testing.T) {
	body := "--frame\r\nContent-Type: image/jpeg\r\n\r\nAAA\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\nBB\r\n--frame--\r\n"
	var frames []string
	require.NoError(t, readFrames(strings.NewReader(body), "frame", func(f []byte) { frames = append(frames, string(f)) }))
	assert.Equal(t, []string{"AAA", "BB"}, frames)
}
