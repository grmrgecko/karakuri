package core

import (
	"context"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/socket"
	"go.uber.org/zap"
)

// SocketInNode starts runs from one settings-owned socket connection.
type SocketInNode struct{ Protocol string }

// SocketOutNode sends a payload through one settings-owned socket connection.
type SocketOutNode struct{ Protocol string }

// TCPRequestNode performs one isolated TCP exchange per execution.
type TCPRequestNode struct{}

func socketLabel(protocol string) string {
	return map[string]string{"tcp": "TCP", "udp": "UDP", "websocket": "WebSocket"}[protocol]
}
func socketConnectionField(protocol string) engine.FieldSpec {
	return engine.FieldSpec{ID: "connection", Label: "Connection", Type: flow.FieldConnection, Required: true, ConnectionKind: protocol}
}
func socketFormatField(id, label string) engine.FieldSpec {
	return engine.FieldSpec{ID: id, Label: label, Type: flow.FieldSelect, Default: "text", Options: []engine.Option{{Value: "text", Label: "Text"}, {Value: "binary", Label: "Binary"}, {Value: "base64", Label: "Base64"}, {Value: "json", Label: "JSON"}}}
}
func socketPortRule(field string) *flow.PortTypeRule {
	return &flow.PortTypeRule{Cases: []flow.PortTypeCase{
		{Match: map[string][]string{field: {"binary"}}, Type: flow.PortBinary},
		{Match: map[string][]string{field: {"json"}}, Type: flow.PortAny},
	}, Default: flow.PortString}
}

// Meta describes the trigger's connection, decoding and test controls.
func (n SocketInNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: n.Protocol + "-in", Label: socketLabel(n.Protocol) + " In", Folder: flow.FolderNetwork, Category: flow.CategoryTrigger, Icon: "las la-sign-in-alt", Description: "Starts a run for each received socket frame", ConnectionKind: n.Protocol,
		Outputs:    []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortString, TypeRule: socketPortRule("format")}},
		ConfigSpec: []engine.FieldSpec{socketInputConnectionField(n.Protocol), socketFormatField("format", "Read payload as"), {ID: "topic", Label: "Topic", Type: flow.FieldString}, {ID: "test_params", Label: "Test payload", Type: flow.FieldJSON, Help: "Seeds Test Run without opening a socket. Binary payloads use base64."}},
	}
}

// Execute forwards a received or test-seeded message.
func (SocketInNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

type socketNodeConfig struct {
	Connection       string `json:"connection"`
	Format           string `json:"format"`
	Topic            string `json:"topic"`
	Target           string `json:"target"`
	Destination      string `json:"destination"`
	AppendDelimiter  string `json:"append_delimiter"`
	CloseAfter       bool   `json:"close_after"`
	ResponseFormat   string `json:"response_format"`
	ResponseMode     string `json:"response_mode"`
	Delimiter        string `json:"delimiter"`
	IncludeDelimiter bool   `json:"include_delimiter"`
	Length           int    `json:"length"`
	MaxBytes         int    `json:"max_bytes"`
	Timeout          string `json:"timeout"`
	Idle             string `json:"idle"`
	CloseWrite       bool   `json:"close_write"`
}

func resolveSocket(ec engine.ExecContext, id, kind string) (*socket.Driver, error) {
	if id == "" {
		return nil, fmt.Errorf("socket connection is required")
	}
	c, e := ec.Connection(id)
	if e != nil {
		return nil, e
	}
	d, ok := c.(*socket.Driver)
	if !ok || d.Kind() != kind {
		return nil, fmt.Errorf("connection must be %s", kind)
	}
	return d, nil
}

// Start follows connection replacements and keeps flow execution off the reader.
func (n SocketInNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg socketNodeConfig
	if e := ec.Config(&cfg); e != nil {
		return e
	}
	if cfg.Connection == "" {
		return fmt.Errorf("socket connection is required")
	}
	if !socketFormatValid(cfg.Format) {
		return fmt.Errorf("invalid payload format")
	}
	for ctx.Err() == nil {
		d, e := resolveSocket(ec, cfg.Connection, n.Protocol)
		var sub *socket.Subscription
		if e == nil {
			sub, e = d.Subscribe()
		}
		if e != nil {
			ec.Logger().Debug("socket input waiting for connection", zap.Error(e))
			select {
			case <-ctx.Done():
				return nil
			case <-ec.Clock().After(time.Second):
			}
			continue
		}
		ec.Logger().Info("socket input armed")
		for ctx.Err() == nil {
			frame, e := sub.Next(ctx)
			if e != nil {
				break
			}
			value, e := socket.Decode(frame.Data, cfg.Format)
			if e != nil {
				ec.Logger().Error("socket input dropped invalid payload", zap.Error(e))
				continue
			}
			if ctx.Err() != nil {
				break
			}
			emit(&engine.Msg{Payload: value, Topic: cfg.Topic, Meta: socketMeta(d, frame)})
		}
		sub.Close()
	}
	return nil
}

func socketMeta(d *socket.Driver, f socket.Frame) map[string]any {
	return map[string]any{"socket_connection_id": d.ID().String(), "socket_session_id": f.SessionID, "socket_protocol": d.Kind(), "remote_address": f.Remote, "local_address": f.Local, "socket_frame_type": f.Type}
}

// Meta describes send formats and explicit target selection.
func (n SocketOutNode) Meta() engine.NodeMeta {
	spec := []engine.FieldSpec{socketInputConnectionField(n.Protocol), socketFormatField("format", "Send payload as")}
	targets := []engine.Option{{Value: "peer", Label: "Configured peer"}, {Value: "reply", Label: "Incoming peer / session"}}
	if n.Protocol != "udp" {
		targets = append(targets, engine.Option{Value: "broadcast", Label: "All listener sessions"})
	}
	spec = append(spec, engine.FieldSpec{ID: "target", Label: "Target", Type: flow.FieldSelect, Default: "peer", Options: targets, Help: "Reply requires metadata from this connection. A stale session fails."})
	if n.Protocol == "udp" {
		spec = append(spec, engine.FieldSpec{ID: "destination", Label: "Destination", Type: flow.FieldString, ShowIf: &flow.ShowIf{Field: "target", Equals: "peer"}, Help: "host:port; empty uses the connection default. Supports {{ }} expressions."})
	}
	if n.Protocol == "tcp" {
		spec = append(spec, engine.FieldSpec{ID: "append_delimiter", Label: "Append delimiter", Type: flow.FieldString, Help: "Optional escaped bytes, such as \\n."}, engine.FieldSpec{ID: "close_after", Label: "Close session after sending", Type: flow.FieldBoolean})
	}
	return engine.NodeMeta{Type: n.Protocol + "-out", Label: socketLabel(n.Protocol) + " Out", Folder: flow.FolderNetwork, Icon: "las la-sign-out-alt", Description: "Sends one payload without automatic replay", ConnectionKind: n.Protocol, Inputs: []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortString, TypeRule: socketPortRule("format")}}, Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()}, ConfigSpec: spec}
}

// Execute sends once and preserves the message envelope on success.
func (n SocketOutNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (out map[string][]*engine.Msg, err error) {
	defer socketNoRetry(&err)
	var cfg socketNodeConfig
	if err = ec.Config(&cfg); err != nil {
		return nil, err
	}
	d, err := resolveSocket(ec, cfg.Connection, n.Protocol)
	if err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	b, binary, err := socket.Encode(msg.Payload, cfg.Format)
	if err != nil {
		return nil, err
	}
	if cfg.AppendDelimiter != "" {
		if n.Protocol != "tcp" {
			return nil, fmt.Errorf("append delimiter requires TCP")
		}
		delim, e := socket.Delimiter(cfg.AppendDelimiter)
		if e != nil {
			return nil, e
		}
		b = append(b[:len(b):len(b)], delim...)
	}
	target := socket.Target{Mode: cfg.Target}
	if cfg.Target == "reply" {
		target.ConnectionID, _ = msg.Meta["socket_connection_id"].(string)
		target.SessionID, _ = msg.Meta["socket_session_id"].(string)
		target.Destination, _ = msg.Meta["remote_address"].(string)
	} else if n.Protocol == "udp" {
		target.Destination, err = ec.Interpolate(cfg.Destination, msg)
		if err != nil {
			return nil, err
		}
	}
	if cfg.CloseAfter && n.Protocol != "tcp" {
		return nil, fmt.Errorf("close after sending requires TCP")
	}
	if err = d.Send(ctx, target, b, binary, cfg.CloseAfter); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// Meta describes the private exchange and its bounded response boundary.
func (TCPRequestNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "tcp-request", Label: "TCP Request", Folder: flow.FolderNetwork, Icon: "las la-exchange-alt", Description: "Sends once on a private TCP socket and reads a bounded response", ConnectionKind: "tcp", Inputs: []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortString, TypeRule: socketPortRule("format")}}, Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortString, TypeRule: socketPortRule("response_format")}, flow.ErrorPort()}, ConfigSpec: []engine.FieldSpec{
		socketRequestConnectionField(), socketFormatField("format", "Send payload as"),
		{ID: "append_delimiter", Label: "Append delimiter", Type: flow.FieldString},
		{ID: "close_write", Label: "Close write side after sending", Type: flow.FieldBoolean, Help: "Signals the end of the request while keeping the response side open."},
		socketFormatField("response_format", "Read response as"),
		{ID: "response_mode", Label: "Response boundary", Type: flow.FieldSelect, Default: "delimiter", Options: []engine.Option{{Value: "delimiter", Label: "Delimiter"}, {Value: "fixed", Label: "Fixed length"}, {Value: "close", Label: "Peer closes"}, {Value: "idle", Label: "Idle gap"}}},
		{ID: "delimiter", Label: "Response delimiter", Type: flow.FieldString, Default: `\n`, ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "delimiter"}},
		{ID: "include_delimiter", Label: "Include delimiter", Type: flow.FieldBoolean, ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "delimiter"}},
		{ID: "length", Label: "Response length", Type: flow.FieldNumber, Default: 1, ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "fixed"}},
		{ID: "idle", Label: "Idle gap", Type: flow.FieldDuration, Default: "200ms", ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "idle"}},
		{ID: "max_bytes", Label: "Maximum response bytes", Type: flow.FieldNumber, Default: 65536},
		{ID: "timeout", Label: "Overall timeout", Type: flow.FieldDuration, Default: "30s", Help: "Includes connection, TLS handshake, send and response."},
	}}
}

// Execute performs an isolated exchange and replaces only the payload and peer metadata.
func (TCPRequestNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (out map[string][]*engine.Msg, err error) {
	defer socketNoRetry(&err)
	var cfg socketNodeConfig
	if err = ec.Config(&cfg); err != nil {
		return nil, err
	}
	if !socketFormatValid(cfg.ResponseFormat) {
		return nil, fmt.Errorf("invalid response format")
	}
	d, err := resolveSocket(ec, cfg.Connection, "tcp")
	if err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	b, _, err := socket.Encode(msg.Payload, cfg.Format)
	if err != nil {
		return nil, err
	}
	if cfg.AppendDelimiter != "" {
		delim, e := socket.Delimiter(cfg.AppendDelimiter)
		if e != nil {
			return nil, e
		}
		b = append(b[:len(b):len(b)], delim...)
	}
	timeout, err := parseDurationDefault(cfg.Timeout, 30*time.Second)
	if err != nil {
		return nil, err
	}
	idle, err := parseDurationDefault(cfg.Idle, 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	mode := cfg.ResponseMode
	if mode == "" {
		mode = "delimiter"
	}
	delimiter := cfg.Delimiter
	if delimiter == "" {
		delimiter = `\n`
	}
	frame, err := d.Request(ctx, b, socket.Framing{Mode: mode, Delimiter: delimiter, IncludeDelimiter: cfg.IncludeDelimiter, Length: cfg.Length, MaxBytes: cfg.MaxBytes, Idle: idle}, timeout, cfg.CloseWrite)
	if err != nil {
		return nil, err
	}
	value, err := socket.Decode(frame.Data, cfg.ResponseFormat)
	if err != nil {
		return nil, err
	}
	msg.Payload = value
	if msg.Meta == nil {
		msg.Meta = make(map[string]any)
	}
	for k, v := range socketMeta(d, frame) {
		msg.Meta[k] = v
	}
	return engine.One("out", msg), nil
}

func socketNoRetry(err *error) {
	if *err != nil {
		*err = &engine.ExecutionError{Message: (*err).Error(), Cause: *err, Retryable: false}
	}
}

func socketInputConnectionField(protocol string) engine.FieldSpec {
	f := socketConnectionField(protocol)
	if protocol == "tcp" {
		f.ConnectionExclude = map[string]string{"mode": "request"}
	}
	return f
}
func socketRequestConnectionField() engine.FieldSpec {
	f := socketConnectionField("tcp")
	f.ConnectionExclude = map[string]string{"mode": "listener"}
	return f
}

func socketFormatValid(format string) bool {
	switch format {
	case "", "text", "binary", "base64", "json":
		return true
	default:
		return false
	}
}
