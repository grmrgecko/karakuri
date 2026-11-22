package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hypebeast/go-osc/osc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// TestMatchAddress pins the OSC 1.0 pattern semantics the address filter
// implements: ? and * stay within one path segment, [] classes support
// ranges and ! negation, and {} alternation matches listed literals.
func TestMatchAddress(t *testing.T) {
	cases := []struct {
		pattern string
		address string
		want    bool
		why     string
	}{
		{"/a/b", "/a/b", true, "literal addresses match themselves"},
		{"/a/b", "/a/c", false, "literal addresses do not match a sibling"},
		{"/a/*", "/a/b", true, "* matches a whole segment"},
		{"/a/*", "/a/b/c", false, "* must not cross a / separator"},
		{"/a/*/c", "/a/b/c", true, "* matches one segment in the middle"},
		{"/mixer/*/level", "/mixer/1/mute", false, "the tail after * still has to match"},
		{"/a/b?", "/a/bc", true, "? matches exactly one character"},
		{"/a/b?", "/a/b", false, "? does not match an empty position"},
		{"/a/b?", "/a/b/", false, "? does not match the / separator"},
		{"/ch[1-4]", "/ch3", true, "[] supports ranges"},
		{"/ch[1-4]", "/ch5", false, "a character outside the range does not match"},
		{"/ch[!1-4]", "/ch5", true, "[!] negates the class"},
		{"/{foo,bar}/x", "/bar/x", true, "{} matches one of the listed literals"},
		{"/{foo,bar}/x", "/baz/x", false, "{} rejects a literal not listed"},
		{"/a/*", "/a/", true, "* matches an empty segment"},
		{"/a", "/a/b", false, "a matched prefix alone is not a match"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, MatchAddress(tc.pattern, tc.address),
			"%q vs %q: %s", tc.pattern, tc.address, tc.why)
	}
}

// TestArgsWireRoundTrip pins the OSC type tags outgoing payloads become and
// what received arguments come back as, through go-osc's real binary codec:
// auto sends whole numbers as int32 ('i') and the rest as float32 ('f'),
// the declared formats force their tag, blobs travel as base64 text on the
// karakuri side, and int32/float32 widen to int64/float64 on receive.
func TestArgsWireRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		payload  any
		format   string
		wantTags string
		wantBack []any
	}{
		{
			name:    "auto splits whole numbers from fractions",
			payload: []any{true, "hi", 1.0, 1.5, nil},
			format:  numberAuto,
			// 1.0 is whole so it goes as int32; 1.5 goes as float32.
			wantTags: ",TsifN",
			wantBack: []any{true, "hi", int64(1), 1.5, nil},
		},
		{
			name:     "float forces the f tag on whole numbers",
			payload:  []any{2.0},
			format:   numberFloat,
			wantTags: ",f",
			wantBack: []any{2.0},
		},
		{
			name:     "double keeps 64-bit precision",
			payload:  []any{1.0000000001},
			format:   numberDouble,
			wantTags: ",d",
			wantBack: []any{1.0000000001},
		},
		{
			name:     "int sends a whole float as int32",
			payload:  []any{3.0},
			format:   numberInt,
			wantTags: ",i",
			wantBack: []any{int64(3)},
		},
		{
			name:     "a scalar payload is a single argument",
			payload:  "solo",
			format:   numberAuto,
			wantTags: ",s",
			wantBack: []any{"solo"},
		},
		{
			name:     "a binary payload sends as a blob and returns as base64",
			payload:  []byte{0x01, 0x02},
			format:   numberAuto,
			wantTags: ",b",
			wantBack: []any{"AQI="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := toOSCArgs(tc.payload, tc.format)
			require.NoError(t, err)
			msg := osc.NewMessage("/t", args...)

			tags, err := msg.TypeTags()
			require.NoError(t, err)
			assert.Equal(t, tc.wantTags, tags, "type tags are the wire contract receivers match on")

			bin, err := msg.MarshalBinary()
			require.NoError(t, err)
			pkt, err := osc.ParsePacket(string(bin))
			require.NoError(t, err)
			back, ok := pkt.(*osc.Message)
			require.True(t, ok, "a message must parse back as a message")
			assert.Equal(t, tc.wantBack, fromOSCArgs(back.Arguments))
		})
	}
}

// pickPort finds a free local UDP port for the driver to bind.
func pickPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	port := pc.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, pc.Close())
	return port
}

// TestDriverReceive exercises the driver against the real wire: a go-osc
// client sends UDP packets to the bound listen port, and only the
// subscription whose filter matches the address sees them. The non-matching
// message is sent first, so its delivery would land before the matching one
// if the filter failed to hold.
func TestDriverReceive(t *testing.T) {
	port := pickPort(t)
	cfgJSON, err := json.Marshal(Config{ListenHost: "127.0.0.1", ListenPort: port})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	require.Eventually(t, func() bool {
		return d.Status().State == integrations.StateConnected
	}, 5*time.Second, 10*time.Millisecond, "the driver must bind its listen port")

	type recv struct {
		address string
		args    []any
	}
	matched := make(chan recv, 1)
	stray := make(chan recv, 1)
	unsub, err := d.Subscribe("/mixer/*/level", func(address string, args []any) {
		matched <- recv{address, args}
	})
	require.NoError(t, err)
	t.Cleanup(unsub)
	unsubStray, err := d.Subscribe("/elsewhere/*", func(address string, args []any) {
		stray <- recv{address, args}
	})
	require.NoError(t, err)
	t.Cleanup(unsubStray)

	client := osc.NewClient("127.0.0.1", port)
	require.NoError(t, client.Send(osc.NewMessage("/mixer/1/mute", int32(1))))
	require.NoError(t, client.Send(osc.NewMessage("/mixer/1/level", float32(0.5), "main")))

	select {
	case got := <-matched:
		assert.Equal(t, "/mixer/1/level", got.address)
		assert.Equal(t, []any{0.5, "main"}, got.args, "received arguments arrive widened for the payload")
	case <-time.After(5 * time.Second):
		t.Fatal("the matching subscription never saw the message")
	}
	assert.Empty(t, stray, "a subscription whose filter does not match must see nothing")
}

// udpBound reports whether a socket holds a loopback UDP port without binding
// it. A datagram sent to an unbound loopback port draws an ICMP port
// unreachable, which the kernel reports on the sending socket's next read as
// ECONNREFUSED; a bound port takes the datagram and the read times out.
func udpBound(t *testing.T, port int) bool {
	t.Helper()
	c, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	require.NoError(t, err)
	defer c.Close()
	if _, err := c.Write([]byte{0}); err != nil {
		return !errors.Is(err, syscall.ECONNREFUSED)
	}
	require.NoError(t, c.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	_, err = c.Read(make([]byte, 1))
	return !errors.Is(err, syscall.ECONNREFUSED)
}

// TestStopDuringBind covers a connection stopped, as a restart does, while the
// listen loop is binding its port. Stop cancels the driver and then closes the
// socket it finds stored; when that ran before the loop stored the socket it
// had just bound, the loop served it forever and the replacement driver could
// never bind the port. Holding the driver's lock keeps the loop between the
// bind and the store while the test cancels the way Stop does.
func TestStopDuringBind(t *testing.T) {
	port := pickPort(t)
	cfgJSON, err := json.Marshal(Config{ListenHost: "127.0.0.1", ListenPort: port})
	require.NoError(t, err)
	drv, err := New(integrations.ConnModel{ID: uuid.New(), Config: cfgJSON}, integrations.Deps{})
	require.NoError(t, err)
	d := drv.(*Driver)

	d.mu.Lock()
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	bound := assert.Eventually(t, func() bool { return udpBound(t, port) }, 5*time.Second, 10*time.Millisecond,
		"the listen loop must bind its port")
	d.cancel()
	d.mu.Unlock()
	require.True(t, bound, "the loop must have bound the port before the test cancels it")

	assert.Eventually(t, func() bool {
		pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return false
		}
		pc.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond, "a socket bound after Stop must be closed so a replacement driver can bind the port")
}
