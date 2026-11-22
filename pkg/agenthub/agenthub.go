// Package agenthub runs the remote-agent subsystem on the host: the private CA,
// the mTLS listener agents dial, enrollment, and the lifecycle of each live
// agent connection. A connected agent's node types are registered through the
// same plugin host the built-ins use, so from the engine's side an agent is one
// more plugin whose transport happens to be a network socket.
package agenthub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
)

// Hub owns the agent listener and the set of live agent sessions.
type Hub struct {
	log       *zap.Logger
	mgr       *pluginhost.Manager
	store     *caStore
	addr      string
	enrollTTL time.Duration
	certTTL   time.Duration

	listener net.Listener
	mu       sync.Mutex
	sessions map[uuid.UUID]*liveSession
	// attaching holds one lock per agent id, taken from dropping the old
	// session through storing the new one.
	attaching map[uuid.UUID]*sync.Mutex
	closing   bool
	eventMu   sync.Mutex
	nextSub   uint64
	eventSub  map[uint64]chan AgentEvent
}

// liveSession tracks one connected agent so it can be dropped on revoke.
type liveSession struct {
	detach func()
	conn   net.Conn
	name   string
}

// New builds a Hub, loading or creating the CA. It does not listen yet.
func New(mgr *pluginhost.Manager, log *zap.Logger, addr string, enrollTTL, certTTL time.Duration) (*Hub, error) {
	if log == nil {
		log = zap.NewNop()
	}
	store, err := loadCAStore(certTTL, log)
	if err != nil {
		return nil, err
	}
	return &Hub{
		log:       log,
		mgr:       mgr,
		store:     store,
		addr:      addr,
		enrollTTL: enrollTTL,
		certTTL:   certTTL,
		sessions:  make(map[uuid.UUID]*liveSession),
		eventSub:  make(map[uint64]chan AgentEvent),
	}, nil
}

// Bundle is the set of roots agents are told to trust, which the token-minting
// handler puts in the join token it hands the operator.
func (h *Hub) Bundle() ca.Bundle { return h.store.Bundle() }

// CAStatus reports the authority's state for the panel.
func (h *Hub) CAStatus() CAStatus { return h.store.Status() }

// RotateCA starts a CA rollover by hand, for a suspected compromise or an
// operator who would rather not wait for the automatic one.
func (h *Hub) RotateCA() error {
	if err := h.store.StartRollover(); err != nil {
		return err
	}
	return h.Maintain()
}

// Maintain advances the rollover state machine: start one when the signing root
// nears expiry, promote the successor once the agents have moved, and drop
// roots nothing needs. The maintenance loop calls it on a timer; anything that
// changes which agents exist can call it to settle the state now.
func (h *Hub) Maintain() error { return h.store.Maintain(agentsMigrated) }

// Addr returns the address the listener bound to, useful when the configured
// port was 0 and the OS chose one.
func (h *Hub) Addr() string {
	if h.listener == nil {
		return h.addr
	}
	return h.listener.Addr().String()
}

// Start opens the listener and accepts agents until Stop. The listener presents
// the host's server leaf, which every agent verifies against the CA it holds —
// from its join token while enrolling, from disk after — and asks for a client
// certificate without requiring one, since an enrolling agent has none yet.
func (h *Hub) Start(ctx context.Context) error {
	if err := h.Maintain(); err != nil {
		return err
	}
	if _, err := h.store.ServerCertificate(); err != nil {
		return err
	}

	// Every accepted connection is configured from the CA's state at that
	// moment, so a leaf reissued after months of uptime and a root added by a
	// rollover both take effect without a restart.
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			cert, err := h.store.ServerCertificate()
			if err != nil {
				return nil, err
			}
			pool := h.store.Pool()
			return &tls.Config{
				MinVersion:   tls.VersionTLS13,
				Certificates: []tls.Certificate{*cert},
				ClientAuth:   tls.VerifyClientCertIfGiven,
				ClientCAs:    pool,
				// A presented client cert is verified to the roots and against
				// the revocation list here; an enrolling agent presents none
				// and is let through to the enrollment exchange.
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return nil
					}
					_, err := ca.VerifyAgentChain(rawCerts, pool, h.serialRevoked)
					return err
				},
			}, nil
		},
	}

	lis, err := tls.Listen("tcp", h.addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("opening agent listener on %s: %w", h.addr, err)
	}
	h.listener = lis
	h.log.Info("agent listener started", zap.String("addr", h.addr))

	go h.acceptLoop()
	go h.maintainLoop(ctx)
	return nil
}

// maintainLoop keeps the authority current while the process runs: it reissues
// the listener certificate before it expires and drives a rollover from one
// root to the next. A server left up for months never has to be restarted to
// stay valid.
func (h *Hub) maintainLoop(ctx context.Context) {
	ticker := time.NewTicker(caMaintainInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			closing := h.closing
			h.mu.Unlock()
			if closing {
				return
			}
			if err := h.Maintain(); err != nil {
				h.log.Warn("agent CA maintenance failed", zap.Error(err))
			}
			// Asking for the certificate is what reissues it when it is due.
			if _, err := h.store.ServerCertificate(); err != nil {
				h.log.Warn("reissuing the agent listener certificate failed", zap.Error(err))
			}
		}
	}
}

// agentsMigrated reports whether every enabled agent that can still renew has
// authenticated with a certificate from the given root. Presenting it proves
// the agent stored it, and the trust bundle that renewal stores first. An agent
// whose leaf already expired cannot renew and has to re-enroll either way, so
// it does not hold up a rollover.
func agentsMigrated(issuerFingerprint string) (bool, error) {
	agents, err := models.ListAgents()
	if err != nil {
		return false, err
	}
	now := time.Now()
	for _, a := range agents {
		if !a.Enabled {
			continue
		}
		if a.CertNotAfter != nil && a.CertNotAfter.Before(now) {
			continue
		}
		if a.PresentedIssuer != issuerFingerprint {
			return false, nil
		}
	}
	return true, nil
}

// acceptLoop hands each accepted connection to a handler.
func (h *Hub) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			h.mu.Lock()
			closing := h.closing
			h.mu.Unlock()
			if closing {
				return
			}
			h.log.Warn("agent accept failed", zap.Error(err))
			continue
		}
		go h.handleConn(conn)
	}
}

// serialRevoked reports whether a certificate serial has been revoked.
func (h *Hub) serialRevoked(serial string) bool {
	revoked, err := models.IsAgentSerialRevoked(serial)
	if err != nil {
		// Fail closed: a lookup error must not admit a possibly-revoked agent.
		h.log.Warn("revocation check failed; refusing", zap.String("serial", serial), zap.Error(err))
		return true
	}
	return revoked
}

// handleConn completes the handshake, reads the mode byte, and dispatches.
func (h *Hub) handleConn(conn net.Conn) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		_ = conn.Close()
		return
	}
	if err := tlsConn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		_ = conn.Close()
		return
	}
	if err := tlsConn.Handshake(); err != nil {
		h.log.Debug("agent handshake failed", zap.Error(err))
		_ = conn.Close()
		return
	}

	mode := make([]byte, 1)
	if _, err := tlsConn.Read(mode); err != nil {
		_ = conn.Close()
		return
	}
	switch mode[0] {
	case agentwire.ModeEnroll:
		h.handleEnroll(tlsConn)
	case agentwire.ModeRenew:
		h.handleRenew(tlsConn)
	case agentwire.ModeConnect:
		h.handleConnect(tlsConn)
	case agentwire.ModeCredentialKey:
		h.handleCredentialKey(tlsConn)
	default:
		h.log.Debug("agent sent unknown mode byte", zap.Int("mode", int(mode[0])))
		_ = conn.Close()
	}
}

// Stop closes the listener and detaches every live agent.
func (h *Hub) Stop() {
	h.mu.Lock()
	h.closing = true
	if h.listener != nil {
		_ = h.listener.Close()
	}
	sessions := make([]*liveSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.sessions = make(map[uuid.UUID]*liveSession)
	h.mu.Unlock()

	for _, s := range sessions {
		s.detach()
	}
}

// DropAgent tears down a live agent connection, used when an operator disables,
// renames, or revokes it. It is a no-op if the agent is not connected.
func (h *Hub) DropAgent(id uuid.UUID) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	if ok {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	if ok {
		s.detach()
		h.publish(AgentEvent{AgentID: id.String(), AgentName: s.name, State: agentDisconnected, At: time.Now()})
	}
}

// dropSession removes one particular connection. An older connection ending
// after a reconnect must not tear down the replacement session.
func (h *Hub) dropSession(id uuid.UUID, expected *liveSession) {
	h.mu.Lock()
	current, ok := h.sessions[id]
	if ok && current == expected {
		delete(h.sessions, id)
	} else {
		ok = false
	}
	h.mu.Unlock()
	if ok {
		current.detach()
		h.publish(AgentEvent{AgentID: id.String(), AgentName: current.name, State: agentDisconnected, At: time.Now()})
	}
}

// IsConnected reports whether an agent currently holds a session.
func (h *Hub) IsConnected(id uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[id]
	return ok
}

// attachLock returns the lock that serializes connect handling for one agent.
// Attaching takes seconds, and two connects for the same id arriving together,
// a cloned host or a restored backup, would otherwise both pass the drop and
// both attach, leaving one session registered nowhere and never torn down.
func (h *Hub) attachLock(id uuid.UUID) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attaching == nil {
		h.attaching = make(map[uuid.UUID]*sync.Mutex)
	}
	lock, ok := h.attaching[id]
	if !ok {
		lock = &sync.Mutex{}
		h.attaching[id] = lock
	}
	return lock
}
