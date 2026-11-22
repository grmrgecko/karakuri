package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// AgentTypePrefix builds the node-type namespace for one agent. Every node an
// agent contributes is registered under agent.<id>.<name>, so two agents never
// collide and a stored flow keeps resolving through a rename or an upgrade,
// which both leave the id untouched.
func AgentTypePrefix(agentID string) string { return "agent." + agentID + "." }

// AgentPluginName is the catalog name under which an agent's contributions
// register, so an unload withdraws exactly what that agent brought.
func AgentPluginName(agentID string) string { return "agent." + agentID }

// AttachAgentConn brings a live agent connection into the plugin catalog. The
// connection is an already-authenticated mTLS stream the agent dialed; this
// runs yamux over it and gRPC in both directions, so the host calls the agent's
// KarakuriPlugin service while the agent calls the host's KarakuriHost service
// back, exactly as a subprocess plugin does over stdio.
//
// The agent's node types are namespaced by its id and grouped in the palette
// under its current name, then registered through the same Host the built-ins
// use. The returned detach func withdraws them and tears the transport down; it
// is safe to call once. The returned done channel closes when the transport
// ends on its own, so the caller can drop the agent without polling the socket.
func (m *Manager) AttachAgentConn(agentID, agentName string, conn net.Conn) (detach func(), done <-chan struct{}, err error) {
	name := AgentPluginName(agentID)

	// Multiplex the single connection. The agent dialed, so it is the yamux
	// client and the host is the server.
	session, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("starting yamux session: %w", err)
	}

	ph := newPluginHost(m.svc, name, m.log)

	// Serve the host callback channel over streams the agent opens. gRPC treats
	// the yamux session as its listener; a stream the agent dials for a
	// KarakuriHost call lands in Accept.
	hostSrv := grpc.NewServer(grpc.MaxRecvMsgSize(plugin.MaxMessageBytes))
	pb.RegisterKarakuriHostServer(hostSrv, agentHost{ph})
	go func() {
		if serveErr := hostSrv.Serve(session); serveErr != nil {
			m.log.Debug("agent host service stopped", zap.String("agent", agentID), zap.Error(serveErr))
		}
	}()

	// Dial the agent's plugin service over streams the host opens. One yamux
	// stream carries every RPC; gRPC multiplexes over its http2 framing.
	clientConn, err := grpc.NewClient("passthrough:///agent",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(plugin.MaxMessageBytes)),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return session.Open()
		}),
	)
	if err != nil {
		hostSrv.Stop()
		_ = session.Close()
		return nil, nil, fmt.Errorf("dialing agent plugin service: %w", err)
	}
	client := pb.NewKarakuriPluginClient(clientConn)

	teardown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout())
		_, _ = client.Shutdown(ctx, &pb.Empty{})
		cancel()
		_ = clientConn.Close()
		hostSrv.Stop()
		ph.releaseAll()
		_ = session.Close()
		_ = conn.Close()
	}

	// Bring the agent up: init, then read what it contributes.
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	initResp, err := client.Init(ctx, &pb.InitRequest{HostVersion: "1"})
	if err != nil {
		teardown()
		return nil, nil, fmt.Errorf("agent init failed: %w", err)
	}
	if !initResp.Success {
		teardown()
		return nil, nil, fmt.Errorf("agent init failed: %s", initResp.Error)
	}
	regs, err := client.GetRegistrations(ctx, &pb.Empty{})
	if err != nil {
		teardown()
		return nil, nil, fmt.Errorf("agent registration query failed: %w", err)
	}

	// Namespace and label every node before registering. The host owns this so
	// an agent cannot collide with another or claim a folder that is not its
	// own, and the agent SDK strips the prefix again when it dispatches.
	if err := namespaceAgentNodes(regs, agentID, agentName); err != nil {
		teardown()
		return nil, nil, err
	}

	lp := &loadedPlugin{
		manifest: ExternalManifest{Name: name},
		rpc:      client,
		host:     ph,
	}
	ext, err := newExternalPlugin(lp, m.svc, regs)
	if err != nil {
		teardown()
		return nil, nil, err
	}
	if err := m.host.Load(ext); err != nil {
		teardown()
		return nil, nil, err
	}
	ph.attach(client)

	m.log.Info("agent attached",
		zap.String("agent", agentID),
		zap.String("name", agentName),
		zap.Int("nodes", len(regs.Nodes)))

	var once bool
	detach = func() {
		if once {
			return
		}
		once = true
		if unloadErr := m.host.Unload(name); unloadErr != nil {
			m.log.Debug("agent had nothing to withdraw", zap.String("agent", agentID), zap.Error(unloadErr))
		}
		teardown()
		m.log.Info("agent detached", zap.String("agent", agentID))
	}
	return detach, session.CloseChan(), nil
}

// agentHost is the callback service an enrolled agent is served. An agent is
// another machine that proved a certificate, not a plugin the operator put
// beside the server, so it gets the run-scoped callbacks its nodes need and
// its own settings, and none of the registrations that reshape the server.
// Seeding role defaults would let a compromised host grant every account
// "api:*"; a permission, provider, or topic-gate registration would let it
// replace a built-in or open the broker to anonymous sockets; broker traffic
// would let it read every topic and spoof connection status; a connection's
// access token would let it act as the account an oauth2 connection signed in.
type agentHost struct {
	*pluginHost
}

// refused answers a callback an agent may not make.
func (agentHost) refused(rpc string) error {
	return status.Errorf(codes.PermissionDenied, "%s is not available to an agent", rpc)
}

func (a agentHost) Publish(context.Context, *pb.PublishRequest) (*pb.Empty, error) {
	return nil, a.refused("Publish")
}

func (a agentHost) Subscribe(context.Context, *pb.TopicRequest) (*pb.ErrorResponse, error) {
	return nil, a.refused("Subscribe")
}

func (a agentHost) Unsubscribe(context.Context, *pb.TopicRequest) (*pb.Empty, error) {
	return nil, a.refused("Unsubscribe")
}

func (a agentHost) RegisterPermission(context.Context, *pb.PermissionRequest) (*pb.Empty, error) {
	return nil, a.refused("RegisterPermission")
}

func (a agentHost) UnregisterPermission(context.Context, *pb.PermissionName) (*pb.Empty, error) {
	return nil, a.refused("UnregisterPermission")
}

func (a agentHost) SeedRoleDefaults(context.Context, *pb.SeedRoleDefaultsRequest) (*pb.ErrorResponse, error) {
	return nil, a.refused("SeedRoleDefaults")
}

func (a agentHost) RegisterProvider(context.Context, *pb.ProviderRequest) (*pb.Empty, error) {
	return nil, a.refused("RegisterProvider")
}

func (a agentHost) UnregisterProvider(context.Context, *pb.ProviderName) (*pb.Empty, error) {
	return nil, a.refused("UnregisterProvider")
}

func (a agentHost) RegisterTopicPermission(context.Context, *pb.TopicPermissionRequest) (*pb.Empty, error) {
	return nil, a.refused("RegisterTopicPermission")
}

func (a agentHost) UnregisterTopicPermission(context.Context, *pb.TopicPermissionName) (*pb.Empty, error) {
	return nil, a.refused("UnregisterTopicPermission")
}

func (a agentHost) ConnectionAccessToken(context.Context, *pb.ConnectionTokenRequest) (*pb.ConnectionTokenResponse, error) {
	return nil, a.refused("ConnectionAccessToken")
}

// namespaceAgentNodes rewrites each node's type into the agent's namespace and
// sets its palette group to the agent's name, leaving the folder the node
// declared for itself so one host's nodes still separate by domain. It mutates
// the registration in place so the shared registration path sees the final
// metadata.
func namespaceAgentNodes(regs *pb.Registrations, agentID, agentName string) error {
	prefix := AgentTypePrefix(agentID)
	for _, n := range regs.Nodes {
		var meta flow.NodeMeta
		if err := json.Unmarshal(n.MetaJson, &meta); err != nil {
			return fmt.Errorf("invalid node metadata from agent: %w", err)
		}
		if !strings.HasPrefix(meta.Type, prefix) {
			meta.Type = prefix + meta.Type
		}
		meta.Group = agentName
		out, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("re-encoding node metadata: %w", err)
		}
		n.MetaJson = out
	}
	// Agents contribute node types only; a kind, port type, or control from an
	// agent would land in a global registry the host cannot namespace, so it is
	// refused rather than silently shared.
	regs.Kinds = nil
	regs.PortTypesJson = nil
	regs.ControlsJson = nil
	regs.AiProvidersJson = nil
	return nil
}
