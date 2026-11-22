package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Ping defaults. Three packets at half-second spacing is enough to tell a
// sleeping host from a lossy link without holding a worker slot for long.
const (
	pingDefaultCount    = 3
	pingDefaultTimeout  = 3 * time.Second
	pingDefaultInterval = 500 * time.Millisecond
	pingMaxCount        = 100
)

// PingNode checks whether a host answers ICMP echo. It reports the result on
// its normal output rather than treating an unreachable host as a node failure,
// so a flow can branch on `alive`; fail_if_down flips that for flows that want
// the failure branch and the node's retry policy to handle a down host.
type PingNode struct{}

func (PingNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ping",
		Label:       "Ping",
		Folder:      flow.FolderNetwork,
		Description: "Checks whether a host answers ICMP echo and reports round-trip times",
		Icon:        "las la-satellite-dish",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{
				// The result is always a set of ping statistics.
				ID: "out", Label: "Out", Type: flow.PortObject,
				TypeRule: &flow.PortTypeRule{PayloadPath: []string{"target"}},
			},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "host",
				Label:       "Host",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "192.168.1.10",
				Help:        "Host name or IP address to ping. {{ }} segments are supported.",
			},
			{
				ID:      "count",
				Label:   "Packets",
				Type:    flow.FieldNumber,
				Default: pingDefaultCount,
				Help:    "How many echo requests to send. More packets measure loss more accurately but hold the node longer.",
			},
			{
				ID:          "timeout",
				Label:       "Reply timeout",
				Type:        flow.FieldDuration,
				Default:     "3s",
				Placeholder: "3s",
				Help:        "How long each packet waits for its reply before counting as lost.",
			},
			{
				ID:          "interval",
				Label:       "Interval",
				Type:        flow.FieldDuration,
				Default:     "500ms",
				Placeholder: "500ms",
				Help:        "Spacing between packets. The node releases its worker slot while waiting.",
			},
			{
				ID:      "ip_version",
				Label:   "IP version",
				Type:    flow.FieldSelect,
				Default: "auto",
				Options: []engine.Option{
					{Value: "auto", Label: "Whichever the name resolves to"},
					{Value: "ipv4", Label: "IPv4 only"},
					{Value: "ipv6", Label: "IPv6 only"},
				},
			},
			{
				ID:      "fail_if_down",
				Label:   "Fail when the host does not answer",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Off reports the result on the normal output with alive false. On fails the node " +
					"instead, so the retry policy re-pings and the failure branch handles a host that stays down.",
			},
			{
				ID:          "target",
				Label:       "Write the result to",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload",
				Help:        "Dot path into the message the result object is written to.",
			},
		},
	}
}

// pingConfig is the decoded ping node configuration.
type pingConfig struct {
	Host       string `json:"host"`
	Count      int    `json:"count"`
	Timeout    string `json:"timeout"`
	Interval   string `json:"interval"`
	IPVersion  string `json:"ip_version"`
	FailIfDown bool   `json:"fail_if_down"`
	Target     string `json:"target"`
}

// pingResult is one ping node run: what was asked, what answered, and the
// round-trip spread over the packets that did.
type pingResult struct {
	host     string
	ip       net.IP
	sent     int
	received int
	rtts     []time.Duration
}

// Execute resolves the host, sends the configured echo requests, and writes the
// result object to the target property.
func (PingNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg pingConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	// Resolve the target.
	host, err := ec.Interpolate(strings.TrimSpace(cfg.Host), msg)
	if err != nil {
		return nil, fmt.Errorf("invalid host template: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("ping node has no host")
	}
	count := cfg.Count
	if count <= 0 {
		count = pingDefaultCount
	}
	if count > pingMaxCount {
		return nil, fmt.Errorf("packet count %d is over the %d limit", count, pingMaxCount)
	}
	timeout, err := parseDurationDefault(cfg.Timeout, pingDefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid reply timeout %q: %w", cfg.Timeout, err)
	}
	interval, err := parseDurationDefault(cfg.Interval, pingDefaultInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
	}

	// Ping it.
	result, err := runPing(ctx, ec, host, cfg.IPVersion, count, timeout, interval)
	if err != nil {
		return nil, err
	}
	ec.Logger().Debug("ping finished",
		zap.String("host", host), zap.String("ip", result.ip.String()),
		zap.Int("sent", result.sent), zap.Int("received", result.received))

	if result.received == 0 && cfg.FailIfDown {
		return nil, fmt.Errorf("no reply from %s (%s) after %d packets", host, result.ip, result.sent)
	}

	// Build the result message.
	target := strings.TrimSpace(cfg.Target)
	if target == "" {
		target = "payload"
	}
	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	if err := setProperty(out, target, result.payload()); err != nil {
		return nil, fmt.Errorf("cannot write the result to %q: %w", target, err)
	}
	out.Meta["host"] = host
	out.Meta["ip"] = result.ip.String()
	return engine.One("out", out), nil
}

// payload renders the result as the object written to the target property.
// The timing fields are only present when something answered, so an expression
// reading avg_ms never sees a misleading zero.
func (r pingResult) payload() map[string]any {
	loss := 100.0
	if r.sent > 0 {
		loss = float64(r.sent-r.received) / float64(r.sent) * 100
	}
	out := map[string]any{
		"host":     r.host,
		"ip":       r.ip.String(),
		"alive":    r.received > 0,
		"sent":     r.sent,
		"received": r.received,
		"loss":     loss,
	}
	if len(r.rtts) == 0 {
		return out
	}
	lowest, highest, total := r.rtts[0], r.rtts[0], time.Duration(0)
	for _, rtt := range r.rtts {
		if rtt < lowest {
			lowest = rtt
		}
		if rtt > highest {
			highest = rtt
		}
		total += rtt
	}
	out["min_ms"] = millis(lowest)
	out["avg_ms"] = millis(total / time.Duration(len(r.rtts)))
	out["max_ms"] = millis(highest)
	return out
}

// millis renders a duration as milliseconds with microsecond resolution, the
// precision a ping report is read at.
func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// runPing sends count echo requests and collects the replies. Waits between
// packets go through Park so the node releases its worker slot; the socket's
// own deadlines are wall-clock, since they bound network I/O rather than
// schedule work.
func runPing(ctx context.Context, ec engine.ExecContext, host, version string, count int, timeout, interval time.Duration) (pingResult, error) {
	ip, err := resolvePingHost(ctx, host, version)
	if err != nil {
		return pingResult{}, err
	}
	result := pingResult{host: host, ip: ip}

	conn, proto, err := listenICMP(ip)
	if err != nil {
		return pingResult{}, err
	}
	defer conn.Close()

	// The kernel rewrites the echo ID on unprivileged datagram sockets, so
	// replies are matched on a per-execution token in the body instead.
	token := uuid.New()
	id := os.Getpid() & 0xffff

	for seq := 0; seq < count; seq++ {
		if err := ctx.Err(); err != nil {
			break
		}
		sentAt, err := sendEcho(conn, ip, proto, id, seq, token[:])
		if err != nil {
			// A send failure with nothing received yet is a real fault (no
			// route, socket closed); once replies exist, treat it as loss.
			if result.received == 0 {
				return pingResult{}, fmt.Errorf("cannot ping %s: %w", ip, err)
			}
			result.sent++
			continue
		}
		result.sent++

		if rtt, ok := awaitReply(conn, proto, seq, token[:], sentAt, timeout); ok {
			result.received++
			result.rtts = append(result.rtts, rtt)
		}

		// Space out the remaining packets, minus whatever the reply already
		// consumed, so the pacing holds whether or not the host answers.
		if seq < count-1 {
			if wait := interval - time.Since(sentAt); wait > 0 {
				if err := ec.Park(ctx, wait); err != nil {
					break
				}
			}
		}
	}
	return result, nil
}

// resolvePingHost resolves a host to one address of the requested family.
func resolvePingHost(ctx context.Context, host, version string) (net.IP, error) {
	network := "ip"
	switch version {
	case "", "auto":
	case "ipv4":
		network = "ip4"
	case "ipv6":
		network = "ip6"
	default:
		return nil, fmt.Errorf("unknown IP version %q", version)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%q resolved to no addresses", host)
	}
	return ips[0], nil
}

// listenICMP opens an ICMP socket for the address family, preferring the
// unprivileged datagram socket and falling back to a raw socket where the
// kernel does not allow it. Returns the connection and the ICMP protocol
// number replies are parsed with.
func listenICMP(ip net.IP) (*icmp.PacketConn, int, error) {
	datagram, raw, proto := "udp4", "ip4:icmp", ipv4.ICMPTypeEcho.Protocol()
	listen := "0.0.0.0"
	if ip.To4() == nil {
		datagram, raw, proto = "udp6", "ip6:ipv6-icmp", ipv6.ICMPTypeEchoRequest.Protocol()
		listen = "::"
	}
	conn, err := icmp.ListenPacket(datagram, listen)
	if err == nil {
		return conn, proto, nil
	}
	conn, rawErr := icmp.ListenPacket(raw, listen)
	if rawErr != nil {
		return nil, 0, fmt.Errorf("cannot open an ICMP socket: %w (raw socket: %v). "+
			"Unprivileged ping needs net.ipv4.ping_group_range to cover this user, or the "+
			"binary needs CAP_NET_RAW", err, rawErr)
	}
	return conn, proto, nil
}

// sendEcho writes one echo request and reports when it went out.
func sendEcho(conn *icmp.PacketConn, ip net.IP, proto, id, seq int, token []byte) (time.Time, error) {
	echoType := icmp.Type(ipv4.ICMPTypeEcho)
	if proto == ipv6.ICMPTypeEchoRequest.Protocol() {
		echoType = ipv6.ICMPTypeEchoRequest
	}
	wire, err := (&icmp.Message{
		Type: echoType,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: token},
	}).Marshal(nil)
	if err != nil {
		return time.Time{}, err
	}

	// Datagram sockets address the peer as UDP, raw sockets as a bare IP.
	var dst net.Addr = &net.UDPAddr{IP: ip}
	if _, ok := conn.LocalAddr().(*net.IPAddr); ok {
		dst = &net.IPAddr{IP: ip}
	}
	sentAt := time.Now()
	if _, err := conn.WriteTo(wire, dst); err != nil {
		return time.Time{}, err
	}
	return sentAt, nil
}

// awaitReply reads until the echo reply carrying this sequence and token
// arrives or the timeout expires. Anything else on the socket — another
// process's traffic on a raw socket, an unreachable notice — is skipped.
func awaitReply(conn *icmp.PacketConn, proto, seq int, token []byte, sentAt time.Time, timeout time.Duration) (time.Duration, bool) {
	deadline := sentAt.Add(timeout)
	buf := make([]byte, 1500)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return 0, false
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, false
		}
		received := time.Now()
		reply, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		echo, ok := reply.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		if echo.Seq != seq || !bytes.Equal(echo.Data, token) {
			continue
		}
		return received.Sub(sentAt), true
	}
}
