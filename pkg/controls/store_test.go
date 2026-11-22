package controls_test

import (
	"fmt"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupStore runs the real migrations against a real SQLite database held in
// memory. The store is the one the server uses, so coercion, persistence, and
// the watcher fan-out below are the production ones rather than stand-ins.
func setupStore(t *testing.T) *controls.Store {
	t.Helper()

	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}), "the tests need a database before a control can exist")
	require.NoError(t, migrate.AutoMigrate(), "the controls table is created by the real migrations")

	// Every test shares the one in-memory database, so clear the table rather
	// than depending on what an earlier test left behind.
	require.NoError(t, db.DB.Exec("DELETE FROM controls").Error, "each test starts from an empty set")
	require.NoError(t, controls.S.Load(), "the store reads the table into memory at startup")
	return controls.S
}

// TestCreateSeedsInitialValue covers what a control holds before anything
// writes to it. A newly created control is shown on a dashboard immediately, so
// it needs a value its kind can hold from the start — a null would render as an
// empty toggle or a blank dropdown that looks broken.
func TestCreateSeedsInitialValue(t *testing.T) {
	s := setupStore(t)

	toggle, err := s.Create("Away", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "a boolean control needs no settings")
	assert.Equal(t, false, toggle.Value, "a boolean starts off, since off is the safe default for a switch")

	mode, err := s.Create("House Mode", controls.KindSelect, "", map[string]any{
		"options": []any{"Home", "Away", "Night"},
	}, false)
	require.NoError(t, err, "a select control with options must be creatable")
	assert.Equal(t, "Home", mode.Value,
		"a select starts on its first option: any other choice would be arbitrary, and a null renders as blank")
}

// TestSetCoercesAndRefuses covers where coercion lives. A control's kind
// decides what it can hold, so text that reads as a number is accepted — an
// MQTT payload and an expression both arrive that way — while a value outside
// the declared bounds is refused rather than stored. The re-read after the
// refusal is the part that matters: a rejected write must leave nothing behind.
func TestSetCoercesAndRefuses(t *testing.T) {
	s := setupStore(t)

	level, err := s.Create("Level", controls.KindNumber, "", map[string]any{
		"min": float64(0), "max": float64(10),
	}, false)
	require.NoError(t, err, "a bounded number control must be creatable")

	got, err := s.Set(level.ID, "7", controls.By(controls.SourceAPI))
	require.NoError(t, err, "text that reads as a number must be accepted: MQTT payloads arrive that way")
	assert.Equal(t, float64(7), got.Value, "the coerced value is stored as a number, not as the text it came in as")

	_, err = s.Set(level.ID, 99, controls.By(controls.SourceAPI))
	assert.Error(t, err, "99 is outside the declared 0..10 and must be refused rather than clamped")

	snap, ok := s.Get(level.ID)
	require.True(t, ok, "the control must still exist after a refused write")
	assert.Equal(t, float64(7), snap.Value, "a refused write must leave the stored value exactly as it was")

	mode, err := s.Create("Mode", controls.KindSelect, "", map[string]any{
		"options": []any{"Home", "Away"},
	}, false)
	require.NoError(t, err, "a select control must be creatable")

	_, err = s.Set(mode.ID, "Vacation", controls.By(controls.SourceAPI))
	assert.Error(t, err,
		"a select can only hold one of its options, or a dashboard would render a value with no matching entry")
}

// TestWatchSeesChangesOnlyWhenValueMoves covers the signal the control-changed
// trigger arms on. A write that does not move the value is not a change, which
// is what stops a device reporting its state every few seconds from running a
// flow every few seconds. The second write is spelled "on" rather than true to
// prove the comparison happens after coercion, not on the raw input.
func TestWatchSeesChangesOnlyWhenValueMoves(t *testing.T) {
	s := setupStore(t)

	toggle, err := s.Create("Porch", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a control to watch")

	var seen []controls.Change
	unwatch := s.Watch(func(c controls.Change) { seen = append(seen, c) })
	defer unwatch()

	_, err = s.Set(toggle.ID, true, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "the first write must be accepted")

	_, err = s.Set(toggle.ID, "on", controls.By(controls.SourceDashboard))
	require.NoError(t, err, `"on" coerces to true, so the write itself must be accepted`)

	require.Len(t, seen, 1,
		`"on" coerces to the value already stored, so the second write is not a change and must not fire a watcher`)
	assert.Equal(t, false, seen[0].Old, "the previous value must be reported, or a flow cannot tell which way it moved")
	assert.Equal(t, true, seen[0].Control.Value, "the new value is what the flow acts on")
	assert.Equal(t, controls.SourceDashboard, seen[0].Source,
		"the source names who wrote it, which is how a flow ignores its own writes")
}

// TestButtonPressAlwaysFires covers the one kind the change rule cannot apply
// to. Pressing Up twice is two events, and suppressing the second because
// "nothing changed" would make a doorbell or a scene button work once.
func TestButtonPressAlwaysFires(t *testing.T) {
	s := setupStore(t)

	opts := map[string]any{"buttons": []any{
		map[string]any{"value": "up", "label": "Up"},
		map[string]any{"value": "down", "label": "Down"},
	}}
	button, err := s.Create("Blinds", controls.KindButtons, "", opts, false)
	require.NoError(t, err, "the test needs a buttons control to press")

	presses := 0
	unwatch := s.Watch(func(controls.Change) { presses++ })
	defer unwatch()

	for i := 0; i < 2; i++ {
		_, err := s.Set(button.ID, "up", controls.By(controls.SourceDashboard))
		require.NoErrorf(t, err, "press %d must be accepted", i)
	}

	assert.Equal(t, 2, presses,
		"two presses in quick succession are two events; swallowing the second would make a scene button work once")

	_, err = s.Set(button.ID, "sideways", controls.By(controls.SourceDashboard))
	assert.Error(t, err,
		"a press naming a button the control does not have is a mistake, not a new button")
}

// TestCompositeAttributeMerge covers what a composite control is for. Setting a
// light's brightness must leave its colour alone: a dashboard sends one
// attribute at a time, and a store that replaced the object would blank
// everything the author did not touch on every slider drag.
func TestCompositeAttributeMerge(t *testing.T) {
	s := setupStore(t)

	opts := map[string]any{"brightness": true, "color": true}
	light, err := s.Create("Desk Lamp", controls.KindLight, "", opts, false)
	require.NoError(t, err, "the test needs a light to dim")

	_, err = s.SetAttribute(light.ID, "color", "#ff8800", controls.By(controls.SourceDashboard))
	require.NoError(t, err, "a light with colour must accept one")

	next, err := s.SetAttribute(light.ID, "brightness", 40, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "brightness is a percentage and 40 is inside it")

	value, ok := next.Value.(map[string]any)
	require.True(t, ok, "a light's value is the object of its attributes")
	assert.Equal(t, "#ff8800", value["color"], "dimming must not discard the colour that was set")
	assert.Equal(t, float64(40), value["brightness"], "the attribute just written is what the dashboard shows")

	_, err = s.SetAttribute(light.ID, "brightness", 140, controls.By(controls.SourceDashboard))
	assert.Error(t, err, "brightness is a percentage, so 140 is not a value the kind can hold")

	_, err = s.SetAttribute(light.ID, "color_temp", 3000, controls.By(controls.SourceDashboard))
	assert.Error(t, err,
		"this light declares no colour temperature, so it has no attribute to set rather than a silent one")
}

// TestResolveByNameAndID covers how a control is addressed. A dashboard row
// stores an id, but a flow author types a name, so both have to resolve — and
// the name match is case-insensitive, since nobody types the capitalisation
// back exactly. Resolving something that does not exist has to miss rather than
// return a zero control, or a flow would write to nothing and look like it
// worked.
func TestResolveByNameAndID(t *testing.T) {
	s := setupStore(t)

	snap, err := s.Create("Kitchen Fan", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a control to resolve")

	byID, ok := s.Resolve(snap.ID.String())
	require.True(t, ok, "a dashboard row stores an id, so an id must resolve")
	assert.Equal(t, "Kitchen Fan", byID.Name, "resolving by id must find the right control")

	byName, ok := s.Resolve("kitchen fan")
	require.True(t, ok, "a flow author types a name, so a name must resolve")
	assert.Equal(t, snap.ID, byName.ID,
		"the name match ignores case: nobody types the capitalisation back exactly")

	_, ok = s.Resolve("nothing")
	assert.False(t, ok,
		"a name nothing matches must miss, or a flow writes to a zero control and appears to have worked")
}

// TestUpdateReseedsWhenOptionsInvalidateValue covers editing a control out from
// under its own value. Dropping the option a select currently holds leaves it
// with a value it can no longer take, which a dashboard would render as an
// empty dropdown; reseeding to the new first option is the only state it can
// legally be in.
func TestUpdateReseedsWhenOptionsInvalidateValue(t *testing.T) {
	s := setupStore(t)

	mode, err := s.Create("Scene", controls.KindSelect, "", map[string]any{
		"options": []any{"Bright", "Dim"},
	}, false)
	require.NoError(t, err, "the test needs a select control")

	_, err = s.Set(mode.ID, "Dim", controls.By(controls.SourceAPI))
	require.NoError(t, err, "the control must hold Dim before the option is removed")

	// Dim is no longer an option, so the control cannot keep it.
	next, err := s.Update(mode.ID, "Scene", controls.KindSelect, "", map[string]any{
		"options": []any{"Bright", "Movie"},
	}, false)
	require.NoError(t, err, "removing an option must be allowed, whatever the control currently holds")
	assert.Equal(t, "Bright", next.Value,
		"the stored value is no longer a legal option, so it reseeds to the new first one rather than rendering blank")
}

// TestScalarAttributeWriteStaysScalar pins the wire shape the dashboard
// widgets use: a row always sends {attribute, value} — "on" for a toggle,
// "value" for a slider — even when the kind is scalar. The store must write
// the bare value, not a one-key object wrapping it, and the change must read
// as a whole-value change or an attribute-filtered trigger never fires.
func TestScalarAttributeWriteStaysScalar(t *testing.T) {
	s := setupStore(t)

	toggle, err := s.Create("Porch Light", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a toggle to flip")

	var seen []controls.Change
	unwatch := s.Watch(func(c controls.Change) { seen = append(seen, c) })
	defer unwatch()

	next, err := s.SetAttribute(toggle.ID, "on", true, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "a toggle's primary attribute must be writable by name")
	assert.Equal(t, true, next.Value,
		"a scalar kind stores the value itself; {\"on\": true} would render as a broken switch everywhere")

	require.Len(t, seen, 1, "the write moved the value, so exactly one change is announced")
	assert.Empty(t, seen[0].Attribute,
		"a scalar write is a whole-value write, so the change carries no attribute")
}

// TestOptionNormalization covers definitions written over the API, which skip
// the settings panel's own seeding and required-field checks. A kind's
// declared defaults must fill in — a climate control with no settings still
// needs modes to be settable at all — and a select with no options must be
// refused at creation rather than accepted as a control that refuses every
// write while looking healthy.
func TestOptionNormalization(t *testing.T) {
	s := setupStore(t)

	thermostat, err := s.Create("Hall Thermostat", controls.KindClimate, "", nil, false)
	require.NoError(t, err, "a climate control with no settings seeds the catalog's default modes")

	_, err = s.SetAttribute(thermostat.ID, "mode", "heat", controls.By(controls.SourceAPI))
	assert.NoError(t, err, "heat is in the default mode list, so the seeded control must accept it")

	_, err = s.Create("Dead Select", controls.KindSelect, "", map[string]any{}, false)
	assert.Error(t, err,
		"a select with no options can never be set; refusing the definition beats storing a dead control")

	_, err = s.Create("Inverted Slider", controls.KindNumber, "", map[string]any{
		"min": 50, "max": 10,
	}, false)
	assert.Error(t, err, "a slider whose floor sits above its ceiling holds no values at all")
}

// TestClimateRangeWriteRefusesAnInvertedPair covers the invariant no single
// attribute owns: each setpoint coerces cleanly on its own, so only the merged
// pair can say the floor sits above the ceiling. The equal pair beside the
// refusal pins the boundary: a zero-width band is still a band.
func TestClimateRangeWriteRefusesAnInvertedPair(t *testing.T) {
	s := setupStore(t)

	hvac, err := s.Create("HVAC", controls.KindClimate, "", map[string]any{"range": true}, false)
	require.NoError(t, err, "a range thermostat is the catalog's own option set plus the range flag")

	_, err = s.SetAttribute(hvac.ID, "target_low", 20, controls.By(controls.SourceAPI))
	require.NoError(t, err, "the first setpoint has nothing to conflict with yet")

	_, err = s.SetAttribute(hvac.ID, "target_high", 18, controls.By(controls.SourceAPI))
	require.Error(t, err, "a ceiling below the stored floor must be refused, though 18 alone is a fine temperature")

	snap, ok := s.Get(hvac.ID)
	require.True(t, ok, "the control must survive a refused write")
	fields := snap.Value.(map[string]any)
	assert.NotContains(t, fields, "target_high", "the refused setpoint must leave nothing behind")

	_, err = s.SetAttribute(hvac.ID, "target_high", 20, controls.By(controls.SourceAPI))
	assert.NoError(t, err, "a ceiling equal to the floor is a zero-width band, not an inversion")
}

// TestDeleteAnnouncesOnTheControlsTopic pins the wire shape the dashboard
// widgets read: a deletion arrives on the control's own topic as its own
// message type with deleted set and no control field, so a widget built before
// deletions were announced ignores it instead of applying it as a value.
func TestDeleteAnnouncesOnTheControlsTopic(t *testing.T) {
	s := setupStore(t)

	c, err := s.Create("Doomed", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the control must exist before it can be deleted")

	var topic, msgType string
	var payload any
	s.SetPublisher(func(tp, mt string, p any) { topic, msgType, payload = tp, mt, p })
	t.Cleanup(func() { s.SetPublisher(nil) })

	require.NoError(t, s.Delete(c.ID), "deleting a stored control must succeed")

	assert.Equal(t, "control:"+c.ID.String(), topic,
		"the deletion rides the control's own topic, the one a widget is already subscribed to")
	assert.Equal(t, "control_deleted", msgType, "the message type distinguishes a deletion from a change")
	fields, ok := payload.(map[string]any)
	require.Truef(t, ok, "payload = %T, want the object the widgets read", payload)
	assert.Equal(t, true, fields["deleted"], "deleted is the flag the widgets branch on")
	assert.NotContains(t, fields, "control",
		"no control field: an older widget reads data.control and must see nothing to apply")
}

// TestDefinitionEditsAreMarked pins what tells a settings edit from a write.
// Creating or editing a control announces it so dashboards redraw, with the
// value unmoved, which for a buttons control is exactly what a repeated press
// looks like. The mark is the only thing a watcher has to tell them apart.
func TestDefinitionEditsAreMarked(t *testing.T) {
	s := setupStore(t)
	opts := map[string]any{"buttons": []any{map[string]any{"value": "up", "label": "Up"}}}

	var seen []controls.Change
	unwatch := s.Watch(func(c controls.Change) { seen = append(seen, c) })
	defer unwatch()

	button, err := s.Create("Blinds", controls.KindButtons, "", opts, false)
	require.NoError(t, err, "the test needs a buttons control")
	_, err = s.Set(button.ID, "up", controls.By(controls.SourceDashboard))
	require.NoError(t, err, "a press of a declared button is accepted")
	_, err = s.Update(button.ID, "Kitchen blinds", controls.KindButtons, "", opts, false)
	require.NoError(t, err, "renaming the control is a valid edit")

	require.Len(t, seen, 3, "the create, the press, and the edit are each announced")
	assert.True(t, seen[0].Definition, "creating a control is a definition change, not a write")
	assert.False(t, seen[1].Definition, "a press is a write")
	assert.True(t, seen[2].Definition, "an edit that leaves the value alone is a definition change, not a press")
}

// TestDefinitionEditKeepsConcurrentWrite locks that editing a control's
// definition cannot undo a value written while the edit was in flight. The
// edit read the stored row, then saved the whole row back without holding the
// write lock, so a write landing between the two was announced to watchers and
// then replaced by the value the edit had read. Each round races one write
// against one rename, and whatever the order, the write must be what remains,
// in memory and in the table.
func TestDefinitionEditKeepsConcurrentWrite(t *testing.T) {
	s := setupStore(t)
	level, err := s.Create("Level", controls.KindNumber, "", map[string]any{
		"min": float64(0), "max": float64(1000),
	}, false)
	require.NoError(t, err)

	for round := 1; round <= 200; round++ {
		want := float64(round)
		done := make(chan error, 2)
		go func() {
			_, err := s.Set(level.ID, want, controls.By(controls.SourceFlow))
			done <- err
		}()
		go func() {
			_, err := s.Update(level.ID, fmt.Sprintf("Level %d", round), controls.KindNumber, "",
				map[string]any{"min": float64(0), "max": float64(1000)}, false)
			done <- err
		}()
		require.NoError(t, <-done)
		require.NoError(t, <-done)

		snap, ok := s.Get(level.ID)
		require.True(t, ok)
		require.Equal(t, want, snap.Value, "round %d: the edit put back the value it read before the write", round)
		require.NoError(t, s.Load())
		stored, _ := s.Get(level.ID)
		require.Equal(t, want, stored.Value, "round %d: the stored value is the one written", round)
	}
}

// TestDeleteIsNotUndoneByConcurrentWrite locks that a write racing a delete
// cannot put the control back. The delete removed the row and then the cached
// control without holding the write lock, so a write that had already read
// the control stored it again afterwards: a control with no row behind it
// that stayed listed, and settable, until the next restart.
func TestDeleteIsNotUndoneByConcurrentWrite(t *testing.T) {
	s := setupStore(t)

	for round := 1; round <= 200; round++ {
		flag, err := s.Create(fmt.Sprintf("Flag %d", round), controls.KindBoolean, "", nil, false)
		require.NoError(t, err)

		done := make(chan struct{}, 2)
		go func() {
			// The write may win or lose the race; either is fine.
			_, _ = s.Set(flag.ID, true, controls.By(controls.SourceFlow))
			done <- struct{}{}
		}()
		go func() {
			assert.NoError(t, s.Delete(flag.ID))
			done <- struct{}{}
		}()
		<-done
		<-done

		_, ok := s.Get(flag.ID)
		require.False(t, ok, "round %d: a deleted control came back", round)
	}
}
