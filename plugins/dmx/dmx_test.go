package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsimonetti/go-artnet/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// TestChannelValues pins how the payload shapes become DMX levels: object
// keys are 1-based channel numbers, lists fill from the start channel,
// booleans read as full and off, fractions round, and anything outside a
// 512-channel byte frame is refused rather than clipped.
func TestChannelValues(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		start   int
		want    map[int]byte
		wantErr string
	}{
		{
			name:    "object maps channels to levels",
			payload: map[string]any{"1": 255.0, "7": 40.0},
			start:   1,
			want:    map[int]byte{1: 255, 7: 40},
		},
		{
			name:    "list fills consecutive channels from the start channel",
			payload: []any{10.0, 20.0, 30.0},
			start:   5,
			want:    map[int]byte{5: 10, 6: 20, 7: 30},
		},
		{
			name:    "a single value sets the start channel alone",
			payload: 127.6,
			start:   3,
			// 127.6 rounds to 128.
			want: map[int]byte{3: 128},
		},
		{
			name:    "booleans read as full and off",
			payload: []any{true, false},
			start:   1,
			want:    map[int]byte{1: 255, 2: 0},
		},
		{
			name:    "a level above 255 is refused",
			payload: map[string]any{"1": 256.0},
			start:   1,
			wantErr: "outside 0 to 255",
		},
		{
			name:    "a channel above 512 is refused",
			payload: map[string]any{"513": 1.0},
			start:   1,
			wantErr: "channels are 1 to 512",
		},
		{
			name:    "a list running past channel 512 is refused",
			payload: []any{1.0, 2.0},
			start:   512,
			wantErr: "run past channel 512",
		},
	}
	for _, tc := range cases {
		got, err := channelValues(tc.payload, tc.start)
		if tc.wantErr != "" {
			assert.ErrorContains(t, err, tc.wantErr, "case %q", tc.name)
			continue
		}
		require.NoError(t, err, "case %q", tc.name)
		assert.Equal(t, tc.want, got, "case %q", tc.name)
	}
}

// pickPort finds a free local UDP port.
func pickPort(t *testing.T) (int, string) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	port := pc.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, pc.Close())
	return port, pc.LocalAddr().String()
}

// TestArtNetWire pins the Art-Net wire contract against the packet codec a
// real node uses: SetChannels emits an ArtDmx packet whose 15-bit
// port-address splits into Net (top 7 bits) and SubUni (low 8), whose
// sequence starts at 1 (0 would disable resequencing), and whose data merges
// channel sets into one full frame.
func TestArtNetWire(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { pc.Close() })

	cfgJSON, err := json.Marshal(Config{Protocol: protoArtNet, Destination: pc.LocalAddr().String()})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)

	// Universe 257 = Net 1, SubUni 1. Two sets on the same universe must
	// merge: the second frame still carries channel 1.
	require.NoError(t, d.SetChannels(257, map[int]byte{1: 255}))
	require.NoError(t, d.SetChannels(257, map[int]byte{3: 40}))

	read := func() *packet.ArtDMXPacket {
		t.Helper()
		buf := make([]byte, 2048)
		require.NoError(t, pc.SetReadDeadline(time.Now().Add(5*time.Second)))
		n, _, err := pc.ReadFrom(buf)
		require.NoError(t, err, "the driver must have sent a frame")
		p, err := packet.Unmarshal(buf[:n])
		require.NoError(t, err, "the frame must parse as Art-Net")
		dmx, ok := p.(*packet.ArtDMXPacket)
		require.True(t, ok, "the frame must be ArtDmx")
		return dmx
	}

	first := read()
	assert.Equal(t, uint8(1), first.Net, "universe 257 puts 1 in the Net field")
	assert.Equal(t, uint8(1), first.SubUni, "universe 257 puts 1 in the SubUni field")
	assert.Equal(t, uint8(1), first.Sequence, "sequence starts at 1; 0 disables resequencing")
	assert.Equal(t, byte(255), first.Data[0], "channel 1 is the first data byte")

	second := read()
	assert.Equal(t, byte(255), second.Data[0], "channel 1 keeps its level across a later set")
	assert.Equal(t, byte(40), second.Data[2], "channel 3 is the third data byte")
}

// TestSACNLoopback runs the sACN send and receive paths against each other
// over real UDP on the loopback: the transmitter unicasts to 127.0.0.1:5568
// where the driver's own receiver listens, and the subscription reports the
// changed channels. Port 5568 is fixed by E1.31, so the test skips when
// something else on the machine holds it.
func TestSACNLoopback(t *testing.T) {
	cfgJSON, err := json.Marshal(Config{Protocol: protoSACN, Destination: "127.0.0.1", BindAddress: "127.0.0.1"})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)

	events := make(chan ReceiveEvent, 4)
	unsub, err := d.Subscribe(1, func(evt ReceiveEvent) { events <- evt })
	if err != nil && strings.Contains(err.Error(), "sacn listen") {
		t.Skipf("port 5568 is in use: %v", err)
	}
	require.NoError(t, err)
	t.Cleanup(unsub)

	require.NoError(t, d.SetChannels(1, map[int]byte{1: 200}))

	select {
	case evt := <-events:
		assert.Equal(t, 1, evt.Universe)
		assert.Equal(t, map[int]byte{1: 200}, evt.Changed, "the first frame diffs against zeros, so only the driven channel reports")
		assert.Equal(t, byte(200), evt.Channels[0])
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription never saw the frame")
	}
}

// TestSACNDestinationLookupRecovers covers a unicast destination that fails
// to resolve on the first send, as a DNS outage does. The universe was already
// activated inside the transmitter by then, so a driver that dropped its
// channel failed every later send with "already activated" and a retry could
// never succeed. The name holds a space, so the resolver rejects it without a
// DNS query; swapping the configured host stands in for DNS recovering.
func TestSACNDestinationLookupRecovers(t *testing.T) {
	cfgJSON, err := json.Marshal(Config{Protocol: protoSACN, Destination: "dmx node.invalid", BindAddress: "127.0.0.1"})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)

	for attempt := 1; attempt <= 2; attempt++ {
		err := d.SetChannels(1, map[int]byte{1: 10})
		require.Errorf(t, err, "send %d must fail while the destination does not resolve", attempt)
		assert.Containsf(t, err.Error(), "dmx node.invalid",
			"send %d must report the destination lookup, not a stale activation: %v", attempt, err)
	}

	d.mu.Lock()
	d.cfg.Destination = "127.0.0.1"
	d.mu.Unlock()
	assert.NoError(t, d.SetChannels(1, map[int]byte{1: 20}), "the send after the destination resolves must succeed")
}
