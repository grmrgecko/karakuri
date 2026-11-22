package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// Handshake is the shared go-plugin handshake config. The cookie is not a
// security measure — go-plugin uses it to refuse plainly wrong executables.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "KARAKURI_PLUGIN",
	MagicCookieValue: "e9c1b7d4-node-graph",
}

// PluginSetName is the key both sides dispense the plugin under.
const PluginSetName = "karakuri"

// GRPCPlugin adapts a pb.KarakuriPluginServer to go-plugin's GRPCPlugin
// interface. The plugin side sets Srv; the host side only dispenses the
// client. Legacy net/rpc mode is unsupported.
type GRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Srv pb.KarakuriPluginServer
}

// GRPCServer registers the plugin implementation on the gRPC server go-plugin
// runs inside the plugin process.
func (p *GRPCPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterKarakuriPluginServer(s, p.Srv)
	if setter, ok := p.Srv.(interface{ SetBroker(*goplugin.GRPCBroker) }); ok {
		setter.SetBroker(broker)
	}
	return nil
}

// ClientHandle is what the host dispenses: the typed client plus the broker
// it uses to serve the callback channel back to the plugin.
type ClientHandle struct {
	Client pb.KarakuriPluginClient
	Broker *goplugin.GRPCBroker
}

// GRPCClient returns the client handle the host uses.
func (p *GRPCPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &ClientHandle{Client: pb.NewKarakuriPluginClient(c), Broker: broker}, nil
}

// MsgToWire encodes an nodeapi.Msg for the wire. A []byte payload rides in its
// own field, because JSON would decode it back as a base64 string.
func MsgToWire(m *nodeapi.Msg) (*pb.WireMsg, error) {
	if m == nil {
		return nil, nil
	}
	w := &pb.WireMsg{
		Id:           m.ID,
		Topic:        m.Topic,
		Continuation: m.Continuation,
	}
	if b, ok := m.Payload.([]byte); ok {
		w.Binary = true
		w.PayloadBinary = b
	} else if m.Payload != nil {
		enc, err := json.Marshal(m.Payload)
		if err != nil {
			return nil, fmt.Errorf("payload cannot be encoded: %w", err)
		}
		w.PayloadJson = enc
	}
	if len(m.Meta) > 0 {
		enc, err := json.Marshal(m.Meta)
		if err != nil {
			return nil, fmt.Errorf("meta cannot be encoded: %w", err)
		}
		w.MetaJson = enc
	}
	if len(m.Group) > 0 {
		enc, err := json.Marshal(m.Group)
		if err != nil {
			return nil, fmt.Errorf("group stack cannot be encoded: %w", err)
		}
		w.GroupJson = enc
	}
	return w, nil
}

// MsgFromWire decodes a wire message back into an nodeapi.Msg.
func MsgFromWire(w *pb.WireMsg) (*nodeapi.Msg, error) {
	if w == nil {
		return nil, nil
	}
	m := &nodeapi.Msg{
		ID:           w.Id,
		Topic:        w.Topic,
		Continuation: w.Continuation,
	}
	switch {
	case w.Binary:
		m.Payload = w.PayloadBinary
	case len(w.PayloadJson) > 0:
		if err := json.Unmarshal(w.PayloadJson, &m.Payload); err != nil {
			return nil, fmt.Errorf("payload cannot be decoded: %w", err)
		}
	}
	if len(w.MetaJson) > 0 {
		if err := json.Unmarshal(w.MetaJson, &m.Meta); err != nil {
			return nil, fmt.Errorf("meta cannot be decoded: %w", err)
		}
	}
	if len(w.GroupJson) > 0 {
		if err := json.Unmarshal(w.GroupJson, &m.Group); err != nil {
			return nil, fmt.Errorf("group stack cannot be decoded: %w", err)
		}
	}
	return m, nil
}

// OutputsToWire encodes a node's output map.
func OutputsToWire(outputs map[string][]*nodeapi.Msg) (map[string]*pb.WireMsgList, error) {
	if outputs == nil {
		return nil, nil
	}
	out := make(map[string]*pb.WireMsgList, len(outputs))
	for port, msgs := range outputs {
		list := &pb.WireMsgList{Msgs: make([]*pb.WireMsg, 0, len(msgs))}
		for _, m := range msgs {
			w, err := MsgToWire(m)
			if err != nil {
				return nil, err
			}
			list.Msgs = append(list.Msgs, w)
		}
		out[port] = list
	}
	return out, nil
}

// OutputsFromWire decodes a node's output map.
func OutputsFromWire(wire map[string]*pb.WireMsgList) (map[string][]*nodeapi.Msg, error) {
	if wire == nil {
		return nil, nil
	}
	out := make(map[string][]*nodeapi.Msg, len(wire))
	for port, list := range wire {
		msgs := make([]*nodeapi.Msg, 0, len(list.GetMsgs()))
		for _, w := range list.GetMsgs() {
			m, err := MsgFromWire(w)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, m)
		}
		out[port] = msgs
	}
	return out, nil
}
