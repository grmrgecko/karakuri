package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func eventsFixture(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "e.db"), MaxOpenConns: 2, MaxIdleConns: 2}))
	require.NoError(t, db.DB.AutoMigrate(&models.Event{}))
	events.R = events.NewRecorder(nil, nil)
	events.R.Start()
	t.Cleanup(func() {
		if events.R != nil {
			events.R.Stop()
		}
		events.R = nil
	})
}

// TestLogEventNode covers FR-010's write: the entry carries the interpolated
// message, level, and the flow and run that wrote it.
func TestLogEventNode(t *testing.T) {
	eventsFixture(t)
	flowActor := events.Actor{Kind: events.ActorFlow, ID: "f1", Name: "Holiday", RunID: "r1", Via: events.ViaFlow}
	ctx := events.WithActor(context.Background(), flowActor)
	in := map[string]*engine.Msg{"in": {Payload: map[string]any{"mode": "holiday"}}}
	out, err := (LogEventNode{}).Execute(ctx, newTestCtx(`{"message":"{{payload.mode}} mode on","level":"warning","include_payload":true}`, nil), in)
	require.NoError(t, err)
	assert.Len(t, out["out"], 1, "the message passes through")

	_, err = (LogEventNode{}).Execute(ctx, newTestCtx(`{"message":"x","level":"loud"}`, nil), in)
	var failure *engine.ExecutionError
	require.ErrorAs(t, err, &failure)
	assert.False(t, failure.Retryable)

	events.R.Stop()
	events.R = nil
	var rows []models.Event
	require.NoError(t, db.DB.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "holiday mode on", rows[0].Message)
	assert.Equal(t, models.EventCustom, rows[0].Category)
	assert.Equal(t, models.EventWarning, rows[0].Level)
	assert.Equal(t, "Holiday", rows[0].ActorName)
	assert.Equal(t, "r1", rows[0].RunID)
	assert.Equal(t, map[string]any{"payload": map[string]any{"mode": "holiday"}}, rows[0].Details)
}

// TestEventLoggedMatching covers FR-010's trigger rules.
func TestEventLoggedMatching(t *testing.T) {
	m, err := eventLoggedConfig{Categories: "connection", Levels: "error", Match: "Garage"}.matcher("f1")
	require.NoError(t, err)
	base := models.Event{Category: models.EventConnection, Level: models.EventError, Message: "garage MQTT failed", ActorKind: events.ActorSystem}
	assert.True(t, m.matches(base))
	other := base
	other.Level = models.EventInfo
	assert.False(t, m.matches(other), "level filter")
	own := base
	own.ActorKind, own.ActorID = events.ActorFlow, "f1"
	assert.False(t, m.matches(own), "a flow never starts itself")

	all, err := eventLoggedConfig{}.matcher("f1")
	require.NoError(t, err)
	assert.False(t, all.matches(models.Event{Category: models.EventSecurity, Message: "Failed sign-in for admin"}),
		"security entries need asking for by name")

	byControl, err := eventLoggedConfig{Control: "c1"}.matcher("")
	require.NoError(t, err)
	assert.True(t, byControl.matches(models.Event{Category: models.EventDevice, SubjectKind: "control", SubjectID: "c1"}))
	assert.False(t, byControl.matches(models.Event{Category: models.EventDevice, SubjectKind: "control", SubjectID: "c2"}))

	_, err = eventLoggedConfig{Categories: "weather"}.matcher("")
	assert.ErrorContains(t, err, `unknown category "weather"`)
}

// TestEventLoggedFires covers the trigger end to end: a new matching entry
// starts one run and its folded repeats start none.
func TestEventLoggedFires(t *testing.T) {
	eventsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := make(chan *engine.Msg, 8)
	done := make(chan error, 1)
	go func() {
		done <- (EventLoggedNode{}).Start(ctx, newTestCtx(`{"categories":"connection"}`, nil), func(m *engine.Msg) { emitted <- m })
	}()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 5; i++ {
		events.Record(events.Entry{Category: models.EventConnection, Level: models.EventError,
			Subject: events.Subject{Kind: "connection", ID: "g"}, Message: "Garage failed"})
	}
	events.Record(events.Entry{Category: models.EventDevice, Message: "ignored"})

	select {
	case m := <-emitted:
		assert.Equal(t, "Garage failed", m.Payload.(map[string]any)["message"])
	case <-time.After(3 * time.Second):
		t.Fatal("the trigger did not fire")
	}
	select {
	case m := <-emitted:
		t.Fatalf("folded repeats start no run, got %v", m.Payload)
	case <-time.After(700 * time.Millisecond):
	}
	cancel()
	require.NoError(t, <-done)
}

// TestEventLoggedTestRunMatches covers testing the node: it outputs the
// newest entry its settings match, not the newest entry of any kind.
func TestEventLoggedTestRunMatches(t *testing.T) {
	eventsFixture(t)
	events.Record(events.Entry{Category: models.EventConnection, Level: models.EventError,
		Subject: events.Subject{Kind: "connection", ID: "g"}, Message: "Garage failed"})
	events.Record(events.Entry{Category: models.EventDevice, Message: "Porch light set to on"})
	events.R.Stop()
	events.R = nil

	out, err := (EventLoggedNode{}).Execute(context.Background(),
		newTestCtx(`{"categories":"connection","match":"garage"}`, nil), nil)
	require.NoError(t, err)
	assert.Equal(t, "Garage failed", out["out"][0].Payload.(map[string]any)["message"])

	_, err = (EventLoggedNode{}).Execute(context.Background(), newTestCtx(`{"categories":"flow"}`, nil), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no recent entry", "nothing matches")
}
