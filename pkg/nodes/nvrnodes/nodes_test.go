package nvrnodes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate/frigatetest"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testCtx is the ExecContext the nodes run under: config, the one
// connection, and interpolation.
type testCtx struct {
	engine.ExecContext
	cfg    string
	driver any
}

func (c testCtx) Config(out any) error           { return json.Unmarshal([]byte(c.cfg), out) }
func (c testCtx) Logger() *zap.Logger            { return zap.NewNop() }
func (c testCtx) Connection(string) (any, error) { return c.driver, nil }
func (c testCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta}))
}

func strp(s string) *string { return &s }

func TestEventFilters(t *testing.T) {
	base := nvr.Event{Camera: "doorbell", Label: "person", Score: 0.8, Stage: nvr.StageStart,
		Zones: []string{"driveway"}, EnteredZones: []string{"driveway"}, SubLabel: strp("Alex")}
	with := func(f func(*nvr.Event)) nvr.Event {
		e := base
		f(&e)
		return e
	}
	tests := []struct {
		name  string
		cfg   eventConfig
		event nvr.Event
		want  bool
	}{
		{"no filters fire on start", eventConfig{}, base, true},
		{"no filters skip updates", eventConfig{}, with(func(e *nvr.Event) { e.Stage = nvr.StageUpdate }), false},
		{"stages", eventConfig{Stages: "start,end"}, with(func(e *nvr.Event) { e.Stage = nvr.StageEnd }), true},
		{"camera", eventConfig{Cameras: "garage"}, base, false},
		{"camera list", eventConfig{Cameras: "garage, doorbell"}, base, true},
		{"label", eventConfig{Labels: "car"}, base, false},
		{"sub label", eventConfig{SubLabel: "alex"}, base, true},
		{"sub label missing", eventConfig{SubLabel: "sam"}, base, false},
		{"score", eventConfig{MinScore: 0.9}, base, false},
		{"entered zone", eventConfig{Zones: "driveway", Stages: "start,update"},
			with(func(e *nvr.Event) { e.Stage = nvr.StageUpdate }), true},
		{"already in zone", eventConfig{Zones: "driveway", Stages: "update"},
			with(func(e *nvr.Event) { e.Stage = nvr.StageUpdate; e.EnteredZones = nil }), false},
		{"end in zone", eventConfig{Zones: "driveway", Stages: "end"},
			with(func(e *nvr.Event) { e.Stage = nvr.StageEnd; e.EnteredZones = nil }), true},
		{"other zone", eventConfig{Zones: "porch"}, base, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.matches(tt.event))
		})
	}
}

func TestReviewFilters(t *testing.T) {
	alert := nvr.Review{Camera: "doorbell", Severity: "alert", Stage: nvr.StageStart}
	detection := nvr.Review{Camera: "doorbell", Severity: "detection", Stage: nvr.StageStart}
	assert.True(t, reviewConfig{}.matches(alert), "alerts by default")
	assert.False(t, reviewConfig{}.matches(detection))
	assert.True(t, reviewConfig{Severity: "any"}.matches(detection))
	assert.False(t, reviewConfig{Cameras: "garage"}.matches(alert))
	assert.False(t, reviewConfig{Stages: "end"}.matches(alert))
}

// startRecorder runs a simulated Frigate and a connected driver.
func startRecorder(t *testing.T) (*frigatetest.Server, *frigate.Driver) {
	t.Helper()
	s := frigatetest.New()
	t.Cleanup(s.Close)
	raw, _ := json.Marshal(frigate.Config{URL: s.URL})
	built, err := frigate.New(integrations.ConnModel{ID: uuid.New(), Name: "nvr", Kind: frigate.Kind, Config: raw}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*frigate.Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateConnected && s.Sockets() == 1 },
		5*time.Second, 10*time.Millisecond)
	return s, d
}

// TestEventTrigger covers the trigger on a simulated server: a person
// entering the driveway fires once, with relay links in the payload.
func TestEventTrigger(t *testing.T) {
	s, d := startRecorder(t)
	emits := make(chan *engine.Msg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = EventTrigger{}.Start(ctx, testCtx{cfg: `{"connection":"x","labels":"person","zones":"driveway","stages":"start,update"}`, driver: d},
			func(m *engine.Msg) { emits <- m })
	}()
	time.Sleep(100 * time.Millisecond)

	id := "1793910167.5-abc123"
	s.Push("events", frigatetest.EventMessage("new", id, "doorbell", "person", 0.86, nil, []string{}))
	s.Push("events", frigatetest.EventMessage("update", id, "doorbell", "person", 0.9, []string{}, []string{"driveway"}))
	s.Push("events", frigatetest.EventMessage("update", id, "doorbell", "person", 0.9, []string{"driveway"}, []string{"driveway"}))
	select {
	case m := <-emits:
		payload := m.Payload.(map[string]any)
		assert.Equal(t, "update", payload["stage"])
		assert.Equal(t, []any{"driveway"}, payload["entered_zones"])
		assert.Equal(t, "/api/connections/"+d.ID().String()+"/nvr/events/"+id+"/clip", payload["clip"])
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger never fired")
	}
	select {
	case extra := <-emits:
		assert.Fail(t, "fired again while the object stayed in the zone", "%v", extra.Payload)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestImageNode covers a camera's frame and an event's snapshot as bytes and
// as an image value, and the failure when the camera has no frames.
func TestImageNode(t *testing.T) {
	s, d := startRecorder(t)
	// The message arrives as one part of a split, as a flow fetching a frame
	// per camera sends it.
	group := []engine.GroupFrame{{ID: "cameras", Index: 1, Count: 3}}
	in := map[string]*engine.Msg{"in": {Payload: map[string]any{"camera": "doorbell", "id": "1793910167.5-abc123"}, Group: group}}

	out, err := ImageNode{}.Execute(context.Background(), testCtx{cfg: `{"connection":"x","source":"camera","camera":"{{ payload.camera }}","height":180}`, driver: d}, in)
	require.NoError(t, err)
	assert.Equal(t, frigatetest.JPEG, out["out"][0].Payload)
	assert.Equal(t, group, out["out"][0].Group, "the bytes must stay in the split they arrived in, or a join after the node never sees its parts")
	assert.Equal(t, group, out["image"][0].Group, "the image value must stay in the split it arrived in")
	assert.Equal(t, "image/jpeg", out["out"][0].Meta["content_type"])
	assert.Equal(t, "image/jpeg", out["image"][0].Payload.(flow.ImageValue).MIME)

	s.AddEvent(map[string]any{"id": "1793910167.5-abc123", "camera": "doorbell", "label": "person", "start_time": 1793910167.5})
	out, err = ImageNode{}.Execute(context.Background(), testCtx{cfg: `{"connection":"x","source":"event","event":"{{ payload.id }}"}`, driver: d}, in)
	require.NoError(t, err)
	assert.Equal(t, frigatetest.JPEG, out["out"][0].Payload)

	_, err = ImageNode{}.Execute(context.Background(), testCtx{cfg: `{"connection":"x","source":"camera","camera":"attic"}`, driver: d}, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no camera "attic"`)
}
