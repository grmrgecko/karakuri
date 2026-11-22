package core

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWOLSendsMagicPacket drives the node over a real UDP socket on loopback,
// which is the only way to know what actually went on the wire. A wake-on-LAN
// packet is fire and forget — nothing answers it — so the bytes leaving the
// host are the entire contract with the machine being woken.
//
// The repeat count is sent as separate datagrams because UDP loses them, and
// both copies have to be well formed rather than the first only.
func TestWOLSendsMagicPacket(t *testing.T) {
	// Listen on loopback so the packet can be inspected.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err, "the test needs a socket to receive the packet on")
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	cfg := `{"mac":"a1:b2:c3:d4:e5:f6","broadcast":"127.0.0.1","port":` +
		strconv.Itoa(port) + `,"repeat":2}`
	out, err := WOLNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": {Payload: "wake"}})
	require.NoError(t, err, "sending to loopback must succeed")

	msg := only(out, "out")
	require.NotNil(t, msg, "the node must emit, or the flow stops at a node that did its work")
	assert.Equal(t, "a1:b2:c3:d4:e5:f6", msg.Meta["mac"],
		"nothing answers a wake packet, so the metadata is the only record of what was addressed")
	assert.Equal(t, 2, msg.Meta["packets"], "the repeat count must be reported: it is how many chances the host got")
	assert.Equal(t, "wake", msg.Payload, "waking a host does not change the message travelling through")

	want := magicPacket(net.HardwareAddr{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6})
	for i := 0; i < 2; i++ {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)),
			"the test must not block forever waiting for packet %d", i)

		buf := make([]byte, 256)
		n, _, err := conn.ReadFrom(buf)
		require.NoErrorf(t, err, "packet %d never arrived; repeats are sent as separate datagrams because UDP loses them", i)
		assert.Equalf(t, want, buf[:n], "packet %d is not a well-formed magic packet", i)
	}
}

// TestMagicPacketLayout pins the wire format defined outside this codebase:
// six 0xFF bytes followed by the target's six-byte address repeated sixteen
// times. That is 6 + 16*6 = 102 bytes, and a network card's firmware matches it
// byte for byte — a packet that is nearly right simply does not wake anything,
// with nothing to observe but a machine that stayed off.
func TestMagicPacketLayout(t *testing.T) {
	hw := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	packet := magicPacket(hw)
	require.Len(t, packet, 102, "the format is a 6-byte header plus the address 16 times: 6 + 16*6 = 102")

	assert.Equal(t, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, packet[:6],
		"the six 0xFF bytes are what a card's firmware scans for")
	for rep := 0; rep < 16; rep++ {
		start := 6 + rep*6
		assert.Equalf(t, []byte(hw), packet[start:start+6],
			"repetition %d must hold the target address; the firmware counts all sixteen", rep)
	}
}

// TestParseMACSeparatorStyles pins the spellings a MAC address is written in
// out in the world. An author copies one out of a router's web page, a label,
// or a Cisco console, and every one of these forms is what some device or tool
// prints. Refusing any of them means the address has to be retyped by hand,
// which is where the typos come from.
func TestParseMACSeparatorStyles(t *testing.T) {
	want := net.HardwareAddr{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6}
	for _, input := range []struct{ text, why string }{
		{"a1:b2:c3:d4:e5:f6", "the colon form most Linux tools print"},
		{"a1-b2-c3-d4-e5-f6", "the hyphen form Windows prints"},
		{"A1:B2:C3:D4:E5:F6", "upper case, as many router pages show it"},
		{"a1b2c3d4e5f6", "bare hex, as printed on a device label"},
		{"a1b2.c3d4.e5f6", "the dotted-quad form Cisco gear prints"},
		{"  a1:b2:c3:d4:e5:f6  ", "surrounding space from a copy and paste"},
	} {
		got, err := parseMAC(input.text)
		if assert.NoErrorf(t, err, "%q must parse: %s", input.text, input.why) {
			assert.Equalf(t, want, got, "%q must resolve to the same address whatever its separators", input.text)
		}
	}

	// The rejections, beside the accepted forms above: a wrong length or a
	// non-hex digit is a typo, and waking the wrong machine is worse than
	// refusing.
	for _, bad := range []string{
		"",
		"not a mac",
		"a1:b2:c3",
		"a1:b2:c3:d4:e5:f6:07:08",
		"zz:b2:c3:d4:e5:f6",
	} {
		_, err := parseMAC(bad)
		assert.Errorf(t, err, "%q is not a six-byte address and must be refused rather than padded or truncated", bad)
	}
}

// TestWOLRejectsBadConfig covers the config the node refuses before opening a
// socket. Nothing answers a wake packet, so a node that sent one to a nonsense
// address would look exactly like a node that worked.
func TestWOLRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
	}{
		{name: "no mac", cfg: `{"broadcast":"127.0.0.1"}`},
		{name: "bad mac", cfg: `{"mac":"nonsense"}`},
		{name: "port out of range", cfg: `{"mac":"a1:b2:c3:d4:e5:f6","port":70000}`},
	}
	for _, c := range cases {
		_, err := WOLNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": {}})
		assert.Errorf(t, err,
			"%s must fail the node: nothing answers a wake packet, so a bad send looks identical to a good one", c.name)
	}
}

// TestWOLInterpolatesMAC covers taking the address from the message rather than
// the config, which is what lets one node wake whichever host a flow decided on
// instead of needing a node per machine.
func TestWOLInterpolatesMAC(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err, "the test needs a socket to send at")
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	cfg := `{"mac":"{{ payload.mac }}","broadcast":"127.0.0.1","port":` + strconv.Itoa(port) + `,"repeat":1}`
	in := &engine.Msg{Payload: map[string]any{"mac": "0a:0b:0c:0d:0e:0f"}}
	out, err := WOLNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "an address supplied by the message must be usable")
	assert.Equal(t, "0a:0b:0c:0d:0e:0f", only(out, "out").Meta["mac"],
		"the address from the payload must be the one addressed, not the template text")
}
