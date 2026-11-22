package dashlive

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStore returns a store over a fixed layout with one placement authored
// hidden on start and one visible, recording what it publishes.
func testStore(t *testing.T) (*Store, uuid.UUID, *[]Message) {
	t.Helper()
	layout := &Layout{Hidden: map[string]bool{"hidden": true, "shown": false}}
	var published []Message
	s := New(func(topic, msgType string, payload any) {
		published = append(published, payload.(Message))
	}, func(uuid.UUID) (*Layout, error) { return layout, nil })
	return s, uuid.New(), &published
}

// TestLocksRequireEveryRelease pins the lock rule the
// feature exists for: a placement hidden by two automations reappears only
// after both release it, a placement authored hidden starts under "start",
// and a forced show clears everything.
func TestLocksRequireEveryRelease(t *testing.T) {
	s, id, _ := testStore(t)
	apply := func(cmd Command) {
		t.Helper()
		_, err := s.Apply(id, cmd)
		require.NoError(t, err)
	}

	apply(Command{Action: ActionHide, Targets: []string{"shown"}, Lock: "presence"})
	apply(Command{Action: ActionHide, Targets: []string{"shown"}, Lock: "night"})
	apply(Command{Action: ActionShow, Targets: []string{"shown"}, Lock: "presence"})
	assert.Equal(t, []string{"night"}, s.Get(id).Locks["shown"], "releasing one of two locks must leave the placement hidden by the other")

	apply(Command{Action: ActionShow, Targets: []string{"hidden"}, Lock: "presence"})
	assert.Equal(t, []string{StartLock}, s.Get(id).Locks["hidden"], "a placement authored hidden keeps its start lock until that lock is released")

	apply(Command{Action: ActionShow, Targets: []string{"hidden", "shown"}, Force: true})
	state := s.Get(id)
	assert.Empty(t, state.Locks["hidden"], "a forced show clears the start lock")
	assert.Empty(t, state.Locks["shown"], "a forced show clears every lock")
	assert.Contains(t, state.Locks, "hidden", "a cleared placement stays recorded so it no longer falls back to its hidden start")
}

// TestInvalidCommandsChangeNothing checks that a refused command neither
// publishes nor alters state, including a placement outside the served
// layout, which would otherwise let typos grow the map.
func TestInvalidCommandsChangeNothing(t *testing.T) {
	s, id, published := testStore(t)
	_, err := s.Apply(id, Command{Action: ActionHide, Targets: []string{"shown"}})
	require.NoError(t, err)
	before := s.Get(id)

	for _, cmd := range []Command{
		{Action: ActionHide, Targets: []string{"not-on-layout"}},
		{Action: ActionAlert},
		{Action: ActionPage, Page: "1"},
		{Action: "explode"},
	} {
		_, err := s.Apply(id, cmd)
		assert.Error(t, err, "command %q must be refused", cmd.Action)
	}
	assert.Equal(t, before, s.Get(id), "refused commands must leave the state untouched")
	assert.Len(t, *published, 1, "only the accepted command is published")
}

// TestAlertsKeepTheNewestFive checks the cap: six alerts leave the last five,
// oldest dropped first.
func TestAlertsKeepTheNewestFive(t *testing.T) {
	s, id, _ := testStore(t)
	forever := 0
	var ids []string
	for i := 0; i < 6; i++ {
		res, err := s.Apply(id, Command{Action: ActionAlert, Title: "door", Duration: &forever})
		require.NoError(t, err)
		ids = append(ids, res.AlertID)
	}
	var kept []string
	for _, a := range s.Get(id).Alerts {
		kept = append(kept, a.ID)
	}
	assert.Equal(t, ids[1:], kept, "the oldest of six alerts is dropped")
}

// TestVariableCommandResolvesOptions pins what a flow may set a variable to. Face
// detection reports a person by name, so a label matches ignoring case and is
// stored as its option value; an empty value returns the variable to its
// default; a value outside the options is refused and leaves the state alone,
// because the options are what the layout grants.
func TestVariableCommandResolvesOptions(t *testing.T) {
	layout := &Layout{Hidden: map[string]bool{}, Variables: []models.DashboardVariable{{
		Name: "person", Options: []models.VariableOption{{Value: "james"}, {Value: "wife", Label: "Sam"}},
	}}}
	s := New(func(string, string, any) {}, func(uuid.UUID) (*Layout, error) { return layout, nil })
	id := uuid.New()
	set := func(value string) error {
		_, err := s.Apply(id, Command{Action: ActionVariable, Variable: "person", Value: json.RawMessage(strconv.Quote(value))})
		return err
	}

	require.NoError(t, set("sam"))
	assert.Equal(t, "wife", s.Get(id).Variables["person"], "a label naming an option stores the option's value")

	assert.Error(t, set("guest"), "a value outside the options must be refused")
	assert.Equal(t, "wife", s.Get(id).Variables["person"], "a refused value must leave the previous one in place")

	_, err := s.Apply(id, Command{Action: ActionVariable, Variable: "room", Value: json.RawMessage(`"kitchen"`)})
	assert.Error(t, err, "a variable the served layout does not define must be refused")

	require.NoError(t, set(""))
	assert.NotContains(t, s.Get(id).Variables, "person", "an empty value returns the variable to its default")
}

// TestVariableCommandResolvesChainedQuery locks that a variable command
// offers a chained query the options scope grants it: the union over every
// value of the variables its query refers to. The command resolved the
// variable on its own, so the query ran with ${host} still written in it,
// returned nothing, and every value was refused as not an option.
func TestVariableCommandResolvesChainedQuery(t *testing.T) {
	previous := dashvars.TimeSeriesOptions
	t.Cleanup(func() { dashvars.TimeSeriesOptions = previous })
	mounts := map[string][]models.VariableOption{
		"alpha": {{Value: "/"}, {Value: "/home"}},
		"beta":  {{Value: "/srv"}},
	}
	dashvars.TimeSeriesOptions = func(_ context.Context, _ *models.VariableQuery, values map[string]models.VariableOption, _ string) dashvars.Result {
		options, ok := mounts[values["host"].Value]
		if !ok {
			return dashvars.Result{Err: errors.New("the selector still holds an unresolved reference")}
		}
		return dashvars.Result{Options: append([]models.VariableOption(nil), options...)}
	}
	layout := &Layout{Hidden: map[string]bool{}, Variables: []models.DashboardVariable{
		{Name: "host", Options: []models.VariableOption{{Value: "alpha"}, {Value: "beta"}}},
		{Name: "mount", Type: models.VariableTypeQuery, Query: &models.VariableQuery{
			Source: models.QueryPrometheusLabelValues, ConnectionID: "c1", Label: "mountpoint",
			Match: `node_filesystem_size_bytes{nodename="${host}"}`,
		}},
	}}
	s := New(func(string, string, any) {}, func(uuid.UUID) (*Layout, error) { return layout, nil })
	id := uuid.New()
	set := func(value string) error {
		_, err := s.Apply(id, Command{Action: ActionVariable, Variable: "mount", Value: json.RawMessage(strconv.Quote(value))})
		return err
	}

	require.NoError(t, set("/srv"), "a value the query returns under one of the host's options is an option")
	assert.Equal(t, "/srv", s.Get(id).Variables["mount"], "the chained variable takes the value the command named")
	assert.Error(t, set("/tmp"), "a value the query returns under no host is still refused")
}
