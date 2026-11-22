package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// wolPacketRepeats is how many copies of the magic packet are sent by default.
// Wake-on-LAN is fire-and-forget over UDP with no acknowledgement, so sending a
// few copies is the accepted way to survive a dropped frame.
const wolPacketRepeats = 3

// WOLNode sends a Wake-on-LAN magic packet. There is no reply to wait for: the
// node reports that the packet went out, not that the machine woke up. Pair it
// with a ping or an HTTP request to confirm.
type WOLNode struct{}

func (WOLNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "wol",
		Label:       "Wake on LAN",
		Folder:      flow.FolderNetwork,
		Description: "Sends a Wake-on-LAN magic packet to a MAC address",
		Icon:        "las la-power-off",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "mac",
				Label:       "MAC address",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "a1:b2:c3:d4:e5:f6",
				Help:        "Target hardware address. Colons, dashes, or dots all work, and {{ }} segments are supported.",
			},
			{
				ID:          "broadcast",
				Label:       "Broadcast address",
				Type:        flow.FieldString,
				Default:     "255.255.255.255",
				Placeholder: "255.255.255.255",
				Help: "Where to send the packet. Use the subnet's broadcast address " +
					"(such as 192.168.1.255) when the target is on another interface " +
					"or a router drops the global broadcast.",
			},
			{
				ID:      "port",
				Label:   "Port",
				Type:    flow.FieldNumber,
				Default: 9,
				Help:    "Wake-on-LAN listens on UDP 9, occasionally 7.",
			},
			{
				ID:      "repeat",
				Label:   "Packet copies",
				Type:    flow.FieldNumber,
				Default: wolPacketRepeats,
				Help:    "How many identical packets to send, since UDP delivery is not guaranteed.",
			},
		},
	}
}

// wolConfig is the decoded Wake-on-LAN node configuration.
type wolConfig struct {
	MAC       string `json:"mac"`
	Broadcast string `json:"broadcast"`
	Port      int    `json:"port"`
	Repeat    int    `json:"repeat"`
}

// Execute builds the magic packet and sends it over UDP.
func (WOLNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg wolConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	// Resolve the target.
	rendered, err := ec.Interpolate(strings.TrimSpace(cfg.MAC), msg)
	if err != nil {
		return nil, fmt.Errorf("invalid MAC template: %w", err)
	}
	hw, err := parseMAC(rendered)
	if err != nil {
		return nil, err
	}
	broadcast := strings.TrimSpace(cfg.Broadcast)
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	broadcast, err = ec.Interpolate(broadcast, msg)
	if err != nil {
		return nil, fmt.Errorf("invalid broadcast template: %w", err)
	}
	port := cfg.Port
	if port == 0 {
		port = 9
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}
	repeat := cfg.Repeat
	if repeat <= 0 {
		repeat = wolPacketRepeats
	}

	// Send the packet.
	packet := magicPacket(hw)
	addr := net.JoinHostPort(broadcast, fmt.Sprint(port))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", addr, err)
	}
	defer conn.Close()

	for i := 0; i < repeat; i++ {
		if _, err := conn.Write(packet); err != nil {
			return nil, fmt.Errorf("sending magic packet to %s: %w", addr, err)
		}
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	meta["mac"] = hw.String()
	meta["broadcast"] = addr
	meta["packets"] = repeat
	ec.Logger().Info("wake-on-lan packet sent")

	return engine.One("out", &engine.Msg{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    meta,
		Group:   msg.Group,
	}), nil
}

// parseMAC accepts the separator styles people actually paste in: colons,
// dashes, dots, or none at all.
func parseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("wol node has no MAC address")
	}
	if hw, err := net.ParseMAC(s); err == nil {
		if len(hw) != 6 {
			return nil, fmt.Errorf("MAC %q must be 6 bytes for Wake-on-LAN", s)
		}
		return hw, nil
	}
	// Fall back to a bare hex string once the common separators are stripped.
	stripped := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(s)
	if len(stripped) != 12 {
		return nil, fmt.Errorf("MAC %q is not a 6-byte hardware address", s)
	}
	hw := make(net.HardwareAddr, 6)
	for i := 0; i < 6; i++ {
		var b byte
		for _, c := range stripped[i*2 : i*2+2] {
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= byte(c - '0')
			case c >= 'a' && c <= 'f':
				b |= byte(c-'a') + 10
			case c >= 'A' && c <= 'F':
				b |= byte(c-'A') + 10
			default:
				return nil, fmt.Errorf("MAC %q contains non-hex characters", s)
			}
		}
		hw[i] = b
	}
	return hw, nil
}

// magicPacket builds the Wake-on-LAN frame: six 0xFF bytes followed by the
// target address repeated sixteen times.
func magicPacket(hw net.HardwareAddr) []byte {
	packet := make([]byte, 0, 6+16*6)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xFF)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}
	return packet
}
