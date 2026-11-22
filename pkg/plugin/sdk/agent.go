package sdk

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// AgentTransport tells ServeAgent where to dial and how to authenticate. The
// TLS config is built per dial rather than handed over once, because an agent
// renews its certificate and picks up new CA roots while it runs: the next
// reconnection has to use what is on disk then, not what was loaded at startup.
// The agent is the dialer, so it works from behind NAT with only an outbound
// path.
type AgentTransport struct {
	ServerAddr string
	TLSConfig  func() (*tls.Config, error)
	Logger     *zap.Logger
}

// ServeAgent runs an agent as a reverse plugin: it dials the host, serves the
// KarakuriPlugin service over a yamux session so the host can execute its
// nodes, and reconnects with backoff when the connection drops. It returns only
// when ctx is canceled.
//
// The plugin is the same plugin.Plugin a subprocess implements; the transport
// is the only difference. The host wires the callback channel before it calls
// Init, so the plugin's ExecContext callbacks reach the host over the same
// session.
func ServeAgent(ctx context.Context, t AgentTransport, p plugin.Plugin) error {
	log := t.Logger
	if log == nil {
		log = stderrLogger()
	}

	// Register once; the registrations do not change between reconnects.
	cap := &capture{}
	if err := p.Register(cap); err != nil {
		return fmt.Errorf("plugin registration failed: %w", err)
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		started := time.Now()
		if err := runSession(ctx, t, p, cap, log); err != nil {
			log.Warn("agent session ended", zap.Error(err))
		} else {
			log.Info("agent disconnected; reconnecting")
			// Only a session that held resets the backoff. The host accepts
			// the TLS connection of a disabled or deleted agent and then
			// closes it, which would otherwise mean a reconnect every second.
			if time.Since(started) >= maxBackoff {
				backoff = time.Second
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// runSession dials, serves, and holds one connection until it ends.
func runSession(ctx context.Context, t AgentTransport, p plugin.Plugin, cap *capture, log *zap.Logger) error {
	tlsCfg, err := t.TLSConfig()
	if err != nil {
		return fmt.Errorf("building TLS configuration: %w", err)
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", t.ServerAddr, tlsCfg)
	if err != nil {
		return fmt.Errorf("dialing host: %w", err)
	}
	closed := false
	closeConn := func() {
		if !closed {
			closed = true
			_ = conn.Close()
		}
	}
	defer closeConn()

	// Announce this is a live connection, not an enrollment.
	if _, err := conn.Write([]byte{agentwire.ModeConnect}); err != nil {
		return fmt.Errorf("sending connect mode: %w", err)
	}

	// The agent dialed, so it is the yamux client.
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		return fmt.Errorf("starting yamux session: %w", err)
	}
	defer session.Close()

	// Dial the host callback service over streams the agent opens.
	hostConn, err := grpc.NewClient("passthrough:///host",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(plugin.MaxMessageBytes)),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return session.Open()
		}),
	)
	if err != nil {
		return fmt.Errorf("dialing host service: %w", err)
	}
	defer hostConn.Close()

	hostClient := pb.NewKarakuriHostClient(hostConn)
	srv := &server{
		plugin:   p,
		manifest: p.Manifest(),
		reg:      cap,
		log:      log,
		drivers:  make(map[string]*runningDriver),
		host:     hostClient,
		api:      &hostAdapter{client: hostClient},
	}

	// Serve the plugin protocol over streams the host opens.
	gs := grpc.NewServer(grpc.MaxRecvMsgSize(plugin.MaxMessageBytes))
	pb.RegisterKarakuriPluginServer(gs, srv)
	go func() {
		if serveErr := gs.Serve(session); serveErr != nil {
			log.Debug("plugin service stopped", zap.Error(serveErr))
		}
	}()

	log.Info("agent connected", zap.String("addr", t.ServerAddr))

	// Hold until the session dies or the process is asked to stop.
	select {
	case <-ctx.Done():
		gs.Stop()
		return ctx.Err()
	case <-session.CloseChan():
		gs.Stop()
		return nil
	}
}
