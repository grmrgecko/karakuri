package core

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPingResultPayload covers the shape a ping result takes in the payload,
// which is what an author writes expressions against. The arithmetic is spelled
// out by the fixture: 2 of 4 packets came back, so loss is 50%, and the two
// round trips of 2ms and 6ms give min 2, avg 4, max 6.
func TestPingResultPayload(t *testing.T) {
	result := pingResult{
		host:     "gateway",
		ip:       net.ParseIP("192.168.1.1"),
		sent:     4,
		received: 2,
		rtts:     []time.Duration{2 * time.Millisecond, 6 * time.Millisecond},
	}
	payload := result.payload()

	assert.Equal(t, true, payload["alive"], "a host that answered at all is alive")
	assert.Equal(t, 2, payload["received"], "both replies must be counted")
	assert.Equal(t, 50.0, payload["loss"], "2 of 4 packets lost is 50 percent")
	assert.Equal(t, 2.0, payload["min_ms"], "the fastest of 2ms and 6ms is 2")
	assert.Equal(t, 4.0, payload["avg_ms"], "(2 + 6) / 2 = 4")
	assert.Equal(t, 6.0, payload["max_ms"], "the slowest of 2ms and 6ms is 6")
}

// TestPingResultPayloadWhenDown covers a host that answered nothing. The
// timing keys are absent rather than zero, which is the interesting half: an
// expression reading avg_ms on a dead host must miss rather than read 0 and
// conclude the host is instantaneous.
func TestPingResultPayloadWhenDown(t *testing.T) {
	payload := pingResult{host: "sleeping", ip: net.ParseIP("10.0.0.9"), sent: 3}.payload()

	assert.Equal(t, false, payload["alive"], "a host that answered nothing is not alive")
	assert.Equal(t, 100.0, payload["loss"], "3 of 3 packets lost is total loss")

	for _, key := range []string{"min_ms", "avg_ms", "max_ms"} {
		assert.NotContainsf(t, payload, key,
			"%s must be absent, not zero: a rule reading it would conclude the dead host replied instantly", key)
	}
}

// TestPingConfigValidation covers the config a ping node refuses before it
// touches the network. Each error has to name the field it is about, because
// the author sees it on the node card with no other clue which of five fields
// is wrong. The packet cap is the one that is not merely a typo: an
// unbounded count would let one node flood a host.
func TestPingConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want string
	}{
		{"no host", `{}`, "no host"},
		{"blank host", `{"host":"   "}`, "no host"},
		{"too many packets", `{"host":"127.0.0.1","count":5000}`, "over the"},
		{"bad timeout", `{"host":"127.0.0.1","timeout":"soon"}`, "reply timeout"},
		{"bad interval", `{"host":"127.0.0.1","interval":"often"}`, "interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ec := newTestCtx(tc.cfg, nil)
			_, err := PingNode{}.Execute(context.Background(), ec, nil)
			require.Error(t, err, "this config cannot be pinged and must be refused before touching the network")
			assert.Containsf(t, err.Error(), tc.want,
				"the error must name what is wrong: the author sees it on the node card with no other clue")
		})
	}
}

// TestPingResolvesHostByVersion covers the IP version setting, which exists
// because a dual-stack name can answer on one family and not the other. Asking
// for a family a host does not have must fail rather than silently fall back —
// a ping that quietly switched families would report the wrong host reachable.
func TestPingResolvesHostByVersion(t *testing.T) {
	ip, err := resolvePingHost(context.Background(), "127.0.0.1", "ipv4")
	require.NoError(t, err, "an IPv4 literal must resolve as IPv4")
	assert.True(t, ip.Equal(net.IPv4(127, 0, 0, 1)), "the literal resolves to itself, got %v", ip)

	_, err = resolvePingHost(context.Background(), "127.0.0.1", "ipv6")
	assert.Error(t, err, "an IPv4 literal has no IPv6 form, so asking for one must fail rather than fall back")

	_, err = resolvePingHost(context.Background(), "127.0.0.1", "quantum")
	assert.Error(t, err, "an unknown IP version must be refused rather than treated as a default")
}

// TestPingLoopback drives the real ICMP path against loopback, which is the
// only way to know the socket, the echo, and the reply match up. It skips where
// the environment grants neither an unprivileged ping socket nor CAP_NET_RAW,
// since that is a property of the host rather than of the code.
//
// Writing to a target path rather than the whole payload is the case worth
// covering: the rest of the message has to survive, or a ping in the middle of
// a flow destroys what it was carrying.
func TestPingLoopback(t *testing.T) {
	// ICMP needs either an unprivileged ping socket or CAP_NET_RAW; skip where
	// the sandbox grants neither rather than failing the suite.
	conn, _, err := listenICMP(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Skipf("no ICMP socket available: %v", err)
	}
	conn.Close()

	ec := newTestCtx(`{"host":"127.0.0.1","count":1,"timeout":"2s","target":"payload.ping"}`, nil)
	in := &engine.Msg{Payload: map[string]any{"keep": true}}
	out, err := PingNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "loopback must answer its own ping")

	msg := only(out, "out")
	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "writing to a path leaves the payload an object")
	assert.Equal(t, true, payload["keep"], "the rest of the payload must survive a ping written to a path")

	result, ok := payload["ping"].(map[string]any)
	require.Truef(t, ok, "the result must land at the configured target path: %v", payload)
	assert.Equal(t, true, result["alive"], "loopback is alive")
	assert.Equal(t, 1, result["received"], "one packet was sent and loopback answers it")
	assert.Equal(t, "127.0.0.1", msg.Meta["ip"],
		"the resolved address belongs in metadata: a flow pinging a name needs to know what it reached")
}

// TestPingFailIfDownRoutesToTheErrorBranch covers the fail_if_down switch
// against its default. A host being down is ordinary information, so by default
// it is reported on the normal output; with the flag set it becomes a node
// failure so the author can wire a failure branch instead of a switch. Both
// halves matter — either alone would leave the flag looking like it did
// nothing.
//
// 192.0.2.0/24 is reserved for documentation and answers nothing anywhere.
func TestPingFailIfDownRoutesToTheErrorBranch(t *testing.T) {
	if conn, _, err := listenICMP(net.IPv4(127, 0, 0, 1)); err != nil {
		t.Skipf("no ICMP socket available: %v", err)
	} else {
		conn.Close()
	}

	cfg := `{"host":"192.0.2.1","count":1,"timeout":"200ms","fail_if_down":true}`
	_, err := PingNode{}.Execute(context.Background(), newTestCtx(cfg, nil), nil)
	assert.Error(t, err, "with fail_if_down set, an unreachable host must fail the node onto its failure branch")

	out, err := PingNode{}.Execute(context.Background(),
		newTestCtx(`{"host":"192.0.2.1","count":1,"timeout":"200ms"}`, nil), nil)
	require.NoError(t, err, "by default a host being down is information, not a failure")

	result, ok := only(out, "out").Payload.(map[string]any)
	require.True(t, ok, "the result is an object")
	assert.Equal(t, false, result["alive"], "the same host must report itself down on the normal output")
}

// TestPingMetaDeclaresErrorPort covers the two things that make ping usable in
// a reliability-focused flow: a declared failure branch to route an unreachable
// host onto, and a retry policy, since a single lost packet on a home network
// says nothing.
func TestPingMetaDeclaresErrorPort(t *testing.T) {
	meta := PingNode{}.Meta()
	ports := flow.NodePorts(meta, &flow.Node{Type: meta.Type}, nil)

	port, ok := ports.ErrorOutput()
	require.Truef(t, ok, "ping must declare a failure branch, or fail_if_down has nowhere to route: %+v", ports.Outputs)
	assert.Equal(t, "error", port.ID, "the failure branch keeps the id edges are stored against")
	assert.True(t, meta.Retryable, "one lost packet on a home network says nothing, so ping must be retryable")
}
