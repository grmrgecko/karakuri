package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/drivers"
	"gitlab.com/gomidi/midi/v2/drivers/testdrv"

	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// TestMessageRoundTrip pins the payload mapping against gomidi's real codec:
// every type encodes to a wire message and decodes back to the same object,
// channels are 1 to 16 on the surface and 0 to 15 on the wire, and the
// required-field and bounds checks refuse rather than clamp.
func TestMessageRoundTrip(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want map[string]any
	}{
		{
			in:   map[string]any{"type": "note_on", "channel": 1.0, "note": 60.0, "velocity": 100.0},
			want: map[string]any{"type": "note_on", "channel": 1, "note": 60, "velocity": 100},
		},
		{
			// Channel 16 is the surface's top channel, wire channel 15.
			in:   map[string]any{"type": "note_off", "channel": 16.0, "note": 0.0, "velocity": 64.0},
			want: map[string]any{"type": "note_off", "channel": 16, "note": 0, "velocity": 64},
		},
		{
			in:   map[string]any{"type": "control_change", "channel": 2.0, "controller": 7.0, "value": 127.0},
			want: map[string]any{"type": "control_change", "channel": 2, "controller": 7, "value": 127},
		},
		{
			in:   map[string]any{"type": "program_change", "channel": 3.0, "program": 12.0},
			want: map[string]any{"type": "program_change", "channel": 3, "program": 12},
		},
		{
			in:   map[string]any{"type": "pitch_bend", "channel": 4.0, "value": -8192.0},
			want: map[string]any{"type": "pitch_bend", "channel": 4, "value": -8192},
		},
		{
			in:   map[string]any{"type": "aftertouch", "channel": 5.0, "pressure": 1.0},
			want: map[string]any{"type": "aftertouch", "channel": 5, "pressure": 1},
		},
		{
			in:   map[string]any{"type": "poly_aftertouch", "channel": 6.0, "note": 61.0, "pressure": 2.0},
			want: map[string]any{"type": "poly_aftertouch", "channel": 6, "note": 61, "pressure": 2},
		},
		{
			// Bytes 1, 2, 3.
			in:   map[string]any{"type": "sysex", "data": "AQID"},
			want: map[string]any{"type": "sysex", "data": "AQID"},
		},
	}
	for _, tc := range cases {
		msg, err := encodeMessage(tc.in)
		require.NoErrorf(t, err, "encoding %v", tc.in["type"])
		assert.Equalf(t, tc.want, decodeMessage(msg), "%v must survive the wire unchanged", tc.in["type"])
	}

	_, err := encodeMessage(map[string]any{"type": "note_on", "channel": 1.0})
	assert.ErrorContains(t, err, `"note" is required`, "a note-on with no note must refuse, not guess")
	_, err = encodeMessage(map[string]any{"type": "note_on", "channel": 17.0, "note": 60.0})
	assert.ErrorContains(t, err, "between 1 and 16", "channel 17 does not exist on the wire")
}

// TestDriverLoopback runs the send and receive paths against each other
// through gomidi's in-memory test driver, injected through the backend seam:
// a sent note comes back through the subscription, and the port names match
// case-insensitively, which is what lets an operator type a fragment of the
// name the backend reports.
func TestDriverLoopback(t *testing.T) {
	orig := midiBackend
	backend := testdrv.New("loop")
	midiBackend = func() (drivers.Driver, error) { return backend, nil }
	t.Cleanup(func() { midiBackend = orig })

	cfgJSON, err := json.Marshal(Config{InputPort: "Loop-In", OutputPort: "LOOP-OUT"})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	require.Eventually(t, func() bool {
		return d.Status().State == integrations.StateConnected
	}, 5*time.Second, 10*time.Millisecond, "both ports must open")

	events := make(chan midi.Message, 4)
	unsub, err := d.Subscribe(func(m midi.Message) { events <- m })
	require.NoError(t, err)
	t.Cleanup(unsub)

	require.NoError(t, d.Send(midi.NoteOn(0, 60, 100)))

	select {
	case msg := <-events:
		assert.Equal(t,
			map[string]any{"type": "note_on", "channel": 1, "note": 60, "velocity": 100},
			decodeMessage(msg), "wire channel 0 surfaces as channel 1")
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription never saw the message")
	}
}

// TestSysExDataMustBeSevenBit pins that SysEx data is refused unless every
// byte is a data byte. Any byte with the high bit set is a status byte, which
// ends the SysEx on the wire, so data pasted with its own F0 and F7 framing
// went out double-framed and the bytes after the first F7 reached the device
// as unrelated messages.
func TestSysExDataMustBeSevenBit(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"framed dump", []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}},
		{"status byte inside", []byte{0x7E, 0x90, 0x3C}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encodeMessage(map[string]any{
				"type": "sysex", "data": base64.StdEncoding.EncodeToString(tc.data),
			})
			assert.ErrorContains(t, err, "sysex data", "a byte above 0x7F cannot travel inside a SysEx message")
		})
	}
}

// gatedDriver wraps a gomidi backend so opening a port blocks until the test
// releases it, holding the open loop inside the open call, and closing a port
// is reported on closed.
type gatedDriver struct {
	drivers.Driver
	entered     chan struct{}
	release     chan struct{}
	closed      chan struct{}
	enteredOnce sync.Once
	closedOnce  sync.Once
}

func newGatedDriver(backend drivers.Driver) *gatedDriver {
	return &gatedDriver{
		Driver:  backend,
		entered: make(chan struct{}),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

// wait blocks the caller inside a port's Open until the test releases it.
func (g *gatedDriver) wait() {
	g.enteredOnce.Do(func() { close(g.entered) })
	<-g.release
}

// markClosed records that a port was closed.
func (g *gatedDriver) markClosed() { g.closedOnce.Do(func() { close(g.closed) }) }

func (g *gatedDriver) Ins() ([]drivers.In, error) {
	ins, err := g.Driver.Ins()
	for i, in := range ins {
		ins[i] = gatedIn{In: in, gate: g}
	}
	return ins, err
}

func (g *gatedDriver) Outs() ([]drivers.Out, error) {
	outs, err := g.Driver.Outs()
	for i, out := range outs {
		outs[i] = gatedOut{Out: out, gate: g}
	}
	return outs, err
}

type gatedIn struct {
	drivers.In
	gate *gatedDriver
}

func (p gatedIn) Open() error  { p.gate.wait(); return p.In.Open() }
func (p gatedIn) Close() error { p.gate.markClosed(); return p.In.Close() }

type gatedOut struct {
	drivers.Out
	gate *gatedDriver
}

func (p gatedOut) Open() error  { p.gate.wait(); return p.Out.Open() }
func (p gatedOut) Close() error { p.gate.markClosed(); return p.Out.Close() }

// TestStopDuringPortOpen covers a connection stopped, as a restart does,
// while the open loop is inside a port's open call. Stop found no port to
// close, so the loop stored the port it then opened, reported connected
// after disabled, and held the device open with no driver left to release it.
func TestStopDuringPortOpen(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "input", cfg: Config{InputPort: "Loop-In"}},
		{name: "output", cfg: Config{OutputPort: "LOOP-OUT"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newGatedDriver(testdrv.New("loop"))
			orig := midiBackend
			midiBackend = func() (drivers.Driver, error) { return gate, nil }
			t.Cleanup(func() { midiBackend = orig })

			cfgJSON, err := json.Marshal(tc.cfg)
			require.NoError(t, err)
			drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
			require.NoError(t, err)
			d := drv.(*Driver)
			require.NoError(t, d.Start(context.Background()))

			select {
			case <-gate.entered:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "the open loop never reached the port's open call")
			}
			d.Stop()
			close(gate.release)

			select {
			case <-gate.closed:
			case <-time.After(2 * time.Second):
				assert.Fail(t, "a port opened after Stop must be closed, since no driver is left to close it")
			}
			assert.Equal(t, integrations.StateDisabled, d.Status().State,
				"a stopped connection must not report connected once the late open finishes")
		})
	}
}
