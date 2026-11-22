// Package sdk is what an external plugin's main imports. A plugin implements
// the same plugin.Plugin interface the built-ins do and hands it to Serve,
// which runs the process side of the wire protocol: it captures the plugin's
// registrations, serves them to the host, executes nodes and triggers against
// a proxy ExecContext whose callbacks reach back into the host, and owns the
// driver instances for any connection kinds the plugin registered.
//
//	func main() { sdk.Serve(myPlugin{}) }
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	goplugin "github.com/hashicorp/go-plugin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// WithoutURL strips the request URL from a transport error. http.Client.Do
// wraps a failure in a *url.Error whose text carries the whole URL, so a
// plugin that sends its API key as a query parameter would otherwise put the
// key into an HTTP error response, a log line, and run history.
func WithoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Serve runs the plugin process. It never returns.
func Serve(p plugin.Plugin) {
	cap := &capture{}
	if err := p.Register(cap); err != nil {
		fmt.Fprintf(os.Stderr, "plugin registration failed: %v\n", err)
		os.Exit(1)
	}
	srv := &server{
		plugin:   p,
		manifest: p.Manifest(),
		reg:      cap,
		log:      stderrLogger(),
		drivers:  make(map[string]*runningDriver),
	}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]goplugin.Plugin{
			plugin.PluginSetName: &plugin.GRPCPlugin{Srv: srv},
		},
		GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
			return grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(plugin.MaxMessageBytes))...)
		},
	})
}

// stderrLogger builds the plugin-side logger. go-plugin forwards the plugin's
// stderr into the host's log stream, so plain console lines are enough.
func stderrLogger() *zap.Logger {
	enc := zap.NewProductionEncoderConfig()
	enc.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(zapcore.NewConsoleEncoder(enc), zapcore.Lock(os.Stderr), zap.InfoLevel)
	return zap.New(core)
}

// capture is the Registrar a plugin registers against inside its own process:
// it records everything so the host can ask for it over the wire.
type capture struct {
	nodes       []nodeapi.NodeType
	kinds       []integrations.ConnectionKind
	portTypes   []flow.PortTypeDef
	controls    []flow.FieldControlDef
	aiProviders []plugin.AIProviderDef
}

func (c *capture) RegisterNode(nt nodeapi.NodeType) { c.nodes = append(c.nodes, nt) }
func (c *capture) RegisterConnectionKind(kind integrations.ConnectionKind) {
	c.kinds = append(c.kinds, kind)
}
func (c *capture) RegisterPortType(def flow.PortTypeDef) { c.portTypes = append(c.portTypes, def) }
func (c *capture) RegisterFieldControl(def flow.FieldControlDef) {
	c.controls = append(c.controls, def)
}
func (c *capture) RegisterAIProvider(def plugin.AIProviderDef) {
	c.aiProviders = append(c.aiProviders, def)
}

func (c *capture) nodeFor(nodeType string) (nodeapi.NodeType, bool) {
	// The host namespaces an agent's nodes as agent.<id>.<name>; the plugin
	// registered them plain. Match the wire type as sent, then again with the
	// namespace stripped, so one dispatch path serves both transports.
	for _, candidate := range []string{nodeType, stripAgentPrefix(nodeType)} {
		for _, nt := range c.nodes {
			if nt.Meta().Type == candidate {
				return nt, true
			}
		}
	}
	return nil, false
}

// stripAgentPrefix removes a leading agent.<id>. namespace, returning the plain
// node name. A type without that shape is returned unchanged.
func stripAgentPrefix(nodeType string) string {
	if !strings.HasPrefix(nodeType, "agent.") {
		return nodeType
	}
	rest := nodeType[len("agent."):]
	dot := strings.IndexByte(rest, '.')
	if dot < 0 {
		return nodeType
	}
	return rest[dot+1:]
}

func (c *capture) kindFor(kind string) (integrations.ConnectionKind, bool) {
	for _, k := range c.kinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return integrations.ConnectionKind{}, false
}

// runningDriver is one live connection driver owned by this plugin process.
type runningDriver struct {
	driver integrations.Driver
	cancel context.CancelFunc
	// statuses relays PublishStatus transitions to the StartDriver stream.
	statuses chan integrations.Status
}

// server implements the plugin side of the wire protocol.
type server struct {
	pb.UnimplementedKarakuriPluginServer

	// plugin is the implementation itself, kept so the optional interfaces
	// (settings, HTTP, messaging) can be asserted at call time.
	plugin   plugin.Plugin
	manifest plugin.Manifest
	reg      *capture
	log      *zap.Logger

	broker *goplugin.GRPCBroker
	host   pb.KarakuriHostClient
	// api is the host callback surface handed to the plugin at Init.
	api plugin.HostAPI

	mu      sync.Mutex
	drivers map[string]*runningDriver
}

// SetBroker receives the broker from the go-plugin glue before serving.
func (s *server) SetBroker(b *goplugin.GRPCBroker) { s.broker = b }

// Init dials the host service back through the broker, then hands it to the
// plugin so it can read settings and register the things that are not node
// types.
func (s *server) Init(ctx context.Context, req *pb.InitRequest) (*pb.InitResponse, error) {
	// The network transport (ServeAgent) wires the host client before Init, so
	// there is no broker to dial back through. The subprocess path dials here.
	if s.host == nil {
		conn, err := s.broker.Dial(req.HostServiceId)
		if err != nil {
			return &pb.InitResponse{Success: false, Error: err.Error()}, nil
		}
		s.host = pb.NewKarakuriHostClient(conn)
		s.api = &hostAdapter{client: s.host}
	}

	if init, ok := s.plugin.(plugin.Initializer); ok {
		if err := init.Init(ctx, s.api); err != nil {
			return &pb.InitResponse{Success: false, Error: err.Error()}, nil
		}
	}
	return &pb.InitResponse{Success: true}, nil
}

// GetSettingsSchema reports the plugin's settings, empty when it has none.
func (s *server) GetSettingsSchema(_ context.Context, _ *pb.Empty) (*pb.SettingsSchema, error) {
	if c, ok := s.plugin.(plugin.Configurable); ok {
		return &pb.SettingsSchema{JsonSchema: c.SettingsSchema()}, nil
	}
	return &pb.SettingsSchema{}, nil
}

// GetOpenAPISpec reports the plugin's API document, empty when it serves none.
func (s *server) GetOpenAPISpec(_ context.Context, _ *pb.Empty) (*pb.OpenAPISpec, error) {
	if o, ok := s.plugin.(plugin.OpenAPIPlugin); ok {
		return &pb.OpenAPISpec{SpecJson: o.OpenAPISpec()}, nil
	}
	return &pb.OpenAPISpec{}, nil
}

// HandleHTTP answers a request the host proxied. A plugin that serves no API
// answers 404 rather than failing the RPC, so the host can tell "no route"
// from "transport broken".
func (s *server) HandleHTTP(ctx context.Context, req *pb.HTTPRequest) (*pb.HTTPResponse, error) {
	h, ok := s.plugin.(plugin.HTTPPlugin)
	if !ok {
		return &pb.HTTPResponse{StatusCode: 404}, nil
	}
	resp, err := h.HandleHTTP(ctx, &plugin.HTTPRequestParams{
		Method:         req.Method,
		Path:           req.Path,
		Headers:        req.Headers,
		Body:           req.Body,
		QueryString:    req.QueryString,
		RemoteAddr:     req.RemoteAddr,
		UserID:         req.UserId,
		Username:       req.Username,
		DashboardGrant: req.DashboardGrant,
		KeyLimited:     req.KeyLimited,
		KeyPermissions: req.KeyPermissions,
	})
	if err != nil {
		return nil, err
	}
	return &pb.HTTPResponse{
		StatusCode: int32(resp.StatusCode),
		Headers:    resp.Headers,
		Body:       resp.Body,
	}, nil
}

// HandleMessage delivers one broker message on a subscribed topic.
func (s *server) HandleMessage(ctx context.Context, req *pb.MessageEvent) (*pb.Empty, error) {
	if h, ok := s.plugin.(plugin.MessagePlugin); ok {
		return &pb.Empty{}, h.HandleMessage(ctx, req.Topic, req.MsgType, req.Payload)
	}
	return &pb.Empty{}, nil
}

// GetRegistrations serializes everything the plugin registered.
func (s *server) GetRegistrations(ctx context.Context, _ *pb.Empty) (*pb.Registrations, error) {
	out := &pb.Registrations{}
	var err error
	if out.ManifestJson, err = json.Marshal(s.manifest); err != nil {
		return nil, err
	}
	for _, nt := range s.reg.nodes {
		meta, err := json.Marshal(nt.Meta())
		if err != nil {
			return nil, err
		}
		_, isTrigger := nt.(nodeapi.TriggerType)
		out.Nodes = append(out.Nodes, &pb.NodeRegistration{MetaJson: meta, Trigger: isTrigger})
	}
	for _, k := range s.reg.kinds {
		spec, err := json.Marshal(k.ConfigSpec)
		if err != nil {
			return nil, err
		}
		out.Kinds = append(out.Kinds, &pb.KindRegistration{
			Kind: k.Kind, Label: k.Label, ConfigSpecJson: spec, Secrets: k.Secrets,
			HasValidate: k.Validate != nil, HasTest: k.Test != nil || k.Probe != nil,
		})
	}
	if len(s.reg.portTypes) > 0 {
		if out.PortTypesJson, err = json.Marshal(s.reg.portTypes); err != nil {
			return nil, err
		}
	}
	if len(s.reg.controls) > 0 {
		if out.ControlsJson, err = json.Marshal(s.reg.controls); err != nil {
			return nil, err
		}
	}
	if len(s.reg.aiProviders) > 0 {
		if out.AiProvidersJson, err = json.Marshal(s.reg.aiProviders); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ExecuteNode runs one node against a proxy ExecContext. A node error rides
// in the response rather than failing the RPC, so the host can tell a failed
// node from a broken transport.
func (s *server) ExecuteNode(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	nt, ok := s.reg.nodeFor(req.NodeType)
	if !ok {
		return &pb.ExecuteResponse{Error: fmt.Sprintf("plugin has no node type %q", req.NodeType)}, nil
	}
	inputs := make(map[string]*nodeapi.Msg, len(req.Inputs))
	for port, w := range req.Inputs {
		m, err := plugin.MsgFromWire(w)
		if err != nil {
			return &pb.ExecuteResponse{Error: err.Error()}, nil
		}
		inputs[port] = m
	}
	ec, err := s.newRemoteCtx(ctx, req.CtxId, req.NodeId, req.ConfigJson, req.PortsJson, req.WiredOutputs)
	if err != nil {
		return &pb.ExecuteResponse{Error: err.Error()}, nil
	}
	outputs, execErr := nt.Execute(ctx, ec, inputs)
	if execErr != nil {
		return &pb.ExecuteResponse{Error: execErr.Error()}, nil
	}
	wire, err := plugin.OutputsToWire(outputs)
	if err != nil {
		return &pb.ExecuteResponse{Error: err.Error()}, nil
	}
	return &pb.ExecuteResponse{Outputs: wire}, nil
}

// StartTrigger arms a trigger: emitted messages stream to the host until
// Start returns. The stream context is the trigger's lifetime — the host
// cancels it to disarm.
func (s *server) StartTrigger(req *pb.StartTriggerRequest, stream pb.KarakuriPlugin_StartTriggerServer) error {
	nt, ok := s.reg.nodeFor(req.NodeType)
	if !ok {
		return fmt.Errorf("plugin has no node type %q", req.NodeType)
	}
	trigger, ok := nt.(nodeapi.TriggerType)
	if !ok {
		return fmt.Errorf("node type %q is not a trigger", req.NodeType)
	}
	ctx := stream.Context()
	ec, err := s.newRemoteCtx(ctx, req.CtxId, req.NodeId, req.ConfigJson, req.PortsJson, req.WiredOutputs)
	if err != nil {
		return err
	}
	var sendMu sync.Mutex
	emit := func(m *nodeapi.Msg) {
		w, err := plugin.MsgToWire(m)
		if err != nil {
			s.log.Error("trigger emit dropped an unencodable message", zap.Error(err))
			return
		}
		sendMu.Lock()
		defer sendMu.Unlock()
		if err := stream.Send(&pb.TriggerEvent{Msg: w}); err != nil {
			s.log.Warn("trigger emit failed to send", zap.Error(err))
		}
	}
	return trigger.Start(ctx, ec, emit)
}

// StartDriver builds and starts a connection driver in this process. The
// first streamed event acks the start; later events are status transitions.
// The driver outlives the stream — StopDriver is what tears it down.
func (s *server) StartDriver(req *pb.StartDriverRequest, stream pb.KarakuriPlugin_StartDriverServer) error {
	kind, ok := s.reg.kindFor(req.Kind)
	if !ok || kind.Factory == nil {
		return fmt.Errorf("plugin has no connection kind %q", req.Kind)
	}
	id, err := uuid.Parse(req.ConnId)
	if err != nil {
		return fmt.Errorf("invalid connection ID %q", req.ConnId)
	}

	rd := &runningDriver{statuses: make(chan integrations.Status, 16)}
	driver, err := kind.Factory(integrations.ConnModel{
		ID: id, Name: req.Name, Kind: req.Kind, Config: req.ConfigJson,
	}, integrations.Deps{
		Logger: s.log.With(zap.String("connection", req.Name), zap.String("kind", req.Kind)),
		PublishStatus: func(status integrations.Status) {
			select {
			case rd.statuses <- status:
			default:
				// A full buffer means the host is not draining; the latest
				// status matters more than a complete history.
				select {
				case <-rd.statuses:
				default:
				}
				rd.statuses <- status
			}
		},
	})
	if err != nil {
		return stream.Send(&pb.DriverEvent{StartError: err.Error()})
	}

	// The driver's lifetime is the plugin process, not the stream: reconnect
	// supervision keeps running while statuses stream to the host.
	dctx, cancel := context.WithCancel(context.Background())
	rd.driver = driver
	rd.cancel = cancel

	s.mu.Lock()
	old := s.drivers[req.ConnId]
	s.drivers[req.ConnId] = rd
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		old.driver.Stop()
	}

	if err := driver.Start(dctx); err != nil {
		s.mu.Lock()
		if s.drivers[req.ConnId] == rd {
			delete(s.drivers, req.ConnId)
		}
		s.mu.Unlock()
		cancel()
		return stream.Send(&pb.DriverEvent{StartError: err.Error()})
	}
	if err := stream.Send(&pb.DriverEvent{}); err != nil {
		return err
	}
	for {
		select {
		case status := <-rd.statuses:
			if err := stream.Send(&pb.DriverEvent{Status: statusToWire(status)}); err != nil {
				return nil
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

// StopDriver tears down the driver for a connection.
func (s *server) StopDriver(ctx context.Context, req *pb.DriverID) (*pb.Empty, error) {
	s.mu.Lock()
	rd := s.drivers[req.ConnId]
	delete(s.drivers, req.ConnId)
	s.mu.Unlock()
	if rd != nil {
		rd.cancel()
		rd.driver.Stop()
	}
	return &pb.Empty{}, nil
}

// ValidateKindConfig runs a kind's Validate hook.
func (s *server) ValidateKindConfig(ctx context.Context, req *pb.KindConfigRequest) (*pb.ErrorResponse, error) {
	kind, ok := s.reg.kindFor(req.Kind)
	if !ok {
		return &pb.ErrorResponse{Error: fmt.Sprintf("plugin has no connection kind %q", req.Kind)}, nil
	}
	if kind.Validate == nil {
		return &pb.ErrorResponse{}, nil
	}
	if err := kind.Validate(req.ConfigJson); err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	return &pb.ErrorResponse{}, nil
}

// TestKindConnection runs a kind's Test hook.
func (s *server) TestKindConnection(ctx context.Context, req *pb.TestConnectionRequest) (*pb.ErrorResponse, error) {
	kind, ok := s.reg.kindFor(req.Kind)
	if !ok || (kind.Test == nil && kind.Probe == nil) {
		return &pb.ErrorResponse{Error: fmt.Sprintf("connection kind %q has no test", req.Kind)}, nil
	}
	id, err := uuid.Parse(req.ConnId)
	if err != nil {
		return &pb.ErrorResponse{Error: fmt.Sprintf("invalid connection ID %q", req.ConnId)}, nil
	}
	if kind.Probe != nil {
		detail, err := kind.Probe(ctx, id, req.ConfigJson)
		if err != nil {
			return &pb.ErrorResponse{Error: err.Error(), Detail: detail}, nil
		}
		return &pb.ErrorResponse{Detail: detail}, nil
	}
	if err := kind.Test(ctx, id, req.ConfigJson); err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	return &pb.ErrorResponse{}, nil
}

// Shutdown lets the plugin release what Init acquired, then stops every
// driver it owns.
func (s *server) Shutdown(ctx context.Context, _ *pb.Empty) (*pb.Empty, error) {
	if sd, ok := s.plugin.(plugin.Shutdowner); ok {
		if err := sd.Shutdown(ctx); err != nil {
			s.log.Warn("plugin shutdown reported an error", zap.Error(err))
		}
	}
	s.mu.Lock()
	drivers := s.drivers
	s.drivers = make(map[string]*runningDriver)
	s.mu.Unlock()
	for _, rd := range drivers {
		rd.cancel()
		rd.driver.Stop()
	}
	return &pb.Empty{}, nil
}

// connectionFor resolves a connection ID to a driver this plugin owns. A
// plugin node can only reach connections of kinds the same plugin registered;
// a cross-process Go interface has nowhere to live.
func (s *server) connectionFor(id string) (any, error) {
	s.mu.Lock()
	rd := s.drivers[id]
	s.mu.Unlock()
	if rd == nil {
		return nil, fmt.Errorf("connection %s is not running in this plugin", id)
	}
	return rd.driver, nil
}

// statusToWire encodes one driver status transition.
func statusToWire(s integrations.Status) *pb.DriverStatus {
	return &pb.DriverStatus{State: s.State, Error: s.Error, Since: s.Since.Format(time.RFC3339Nano)}
}
