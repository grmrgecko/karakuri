package agenthub

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// handleCredentialKey answers an enrolled agent's request for its
// credential-store master key on a mutually authenticated connection. The key
// is the envelope key: the agent's on-disk credential store is sealed with it,
// so handing it back is what lets the agent decrypt a stored account password
// for a credentialed unlock — and withholding it is what keeps a stolen store
// file inert. The host reads which agent from the certificate, never from what
// the agent claims.
func (h *Hub) handleCredentialKey(conn *tls.Conn) {
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return
	}
	idStr, ok := ca.AgentIDFromCert(state.PeerCertificates[0])
	if !ok {
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return
	}

	agent, err := models.FindAgentByID(id)
	if err != nil || !agent.Enabled {
		_ = agentwire.WriteJSON(conn, agentwire.CredentialKeyResponse{Error: "unknown or disabled agent"})
		return
	}

	key, err := models.AgentCredentialKey(id)
	if err != nil {
		h.log.Warn("credential key request failed", zap.String("agent", idStr), zap.Error(err))
		_ = agentwire.WriteJSON(conn, agentwire.CredentialKeyResponse{Error: "credential key unavailable"})
		return
	}
	_ = agentwire.WriteJSON(conn, agentwire.CredentialKeyResponse{Key: base64.StdEncoding.EncodeToString(key)})
	for i := range key {
		key[i] = 0
	}
}

// handleEnroll validates a token, signs the agent's CSR, and creates its record.
// It runs before the agent holds any certificate, so the only credential is the
// single-use token; the agent has already verified the host against the CA its
// join token carried.
func (h *Hub) handleEnroll(conn *tls.Conn) {
	defer conn.Close()

	var req agentwire.EnrollRequest
	if err := agentwire.ReadJSON(conn, &req); err != nil {
		h.log.Debug("reading enroll request", zap.Error(err))
		return
	}

	resp, err := h.enroll(req)
	if err != nil {
		h.log.Info("enrollment refused", zap.Error(err))
		_ = agentwire.WriteJSON(conn, agentwire.EnrollResponse{Error: err.Error()})
		return
	}
	if err := agentwire.WriteJSON(conn, resp); err != nil {
		h.log.Debug("writing enroll response", zap.Error(err))
	}
}

// enroll performs the record-and-sign half of enrollment.
func (h *Hub) enroll(req agentwire.EnrollRequest) (agentwire.EnrollResponse, error) {
	tok, err := models.ConsumeEnrollmentToken(req.Token, time.Now())
	if err != nil {
		return agentwire.EnrollResponse{}, err
	}

	// Name the agent: the token's name wins, since the operator minting it chose
	// it deliberately, then the agent's own preference, then its hostname.
	// Collisions get a suffix so the unique index never rejects a legitimate
	// enrollment.
	name := firstNonEmpty(tok.Name, req.Name, req.Hostname)
	agent := &models.Agent{
		Name:        uniqueAgentName(name),
		Description: tok.Description,
		Hostname:    req.Hostname,
		OS:          req.OS,
		Arch:        req.Arch,
		Version:     req.Version,
		Enabled:     true,
		// The user who minted the token owns what enrolls with it.
		OwnerID: tok.CreatedBy,
	}
	if err := models.SaveAgent(agent); err != nil {
		return agentwire.EnrollResponse{}, fmt.Errorf("creating agent record: %w", err)
	}

	// Sign with the newest root, so an agent enrolling during a rollover starts
	// on the incoming CA rather than one that is about to retire.
	issuer := h.store.Issuer()
	certPEM, err := issuer.SignAgent([]byte(req.CSR), agent.ID.String(), h.certTTL)
	if err != nil {
		_ = models.DeleteAgent(agent.ID)
		return agentwire.EnrollResponse{}, fmt.Errorf("signing agent certificate: %w", err)
	}
	h.recordIssued(agent.ID, certPEM, issuer)

	h.log.Info("agent enrolled",
		zap.String("agent", agent.ID.String()),
		zap.String("name", agent.Name),
		zap.String("hostname", req.Hostname))
	events.Record(events.Entry{
		Category: models.EventAgent,
		Subject:  events.Subject{Kind: "agent", ID: agent.ID.String(), Name: agent.Name},
		Message:  agent.Name + " enrolled from " + req.Hostname,
		Details:  map[string]any{"hostname": req.Hostname, "os": req.OS, "arch": req.Arch},
		Actor:    events.Actor{Kind: events.ActorAgent, ID: agent.ID.String(), Name: agent.Name},
	})

	return agentwire.EnrollResponse{
		Cert:      string(certPEM),
		CA:        string(h.store.Bundle().PEM()),
		AgentID:   agent.ID.String(),
		AgentName: agent.Name,
	}, nil
}

// handleRenew reissues a certificate for an agent that is already enrolled. The
// connection is mutually authenticated, so the agent's identity comes from the
// certificate it presented rather than from anything it claims, and the whole
// exchange is one round trip an agent makes on a timer whether or not anything
// is due — the host is the one that knows.
func (h *Hub) handleRenew(conn *tls.Conn) {
	defer conn.Close()

	var req agentwire.RenewRequest
	if err := agentwire.ReadJSON(conn, &req); err != nil {
		h.log.Debug("reading renew request", zap.Error(err))
		return
	}
	resp, presented, err := h.renew(conn, req)
	if err != nil {
		h.log.Info("renewal refused", zap.Error(err))
		_ = agentwire.WriteJSON(conn, agentwire.RenewResponse{Error: err.Error()})
		return
	}
	if err := agentwire.WriteJSON(conn, resp); err != nil {
		h.log.Debug("writing renew response", zap.Error(err))
	}
	// The agent may have just shown the last successor certificate a rollover
	// was waiting on, and waiting half a day to notice would leave the old root
	// signing for no reason.
	if presented {
		if err := h.Maintain(); err != nil {
			h.log.Warn("agent CA maintenance failed", zap.Error(err))
		}
	}
}

// renew decides what the agent is owed: a fresh certificate when its own is
// near expiry or was signed by a root being rolled away from, and the trust
// bundle when the one it holds is out of date. Both follow what the agent
// presented and reported rather than what was last issued, so a reply lost in
// delivery or storage is simply issued again. presented reports whether the
// handshake moved the agent's presented issuer.
func (h *Hub) renew(conn *tls.Conn, req agentwire.RenewRequest) (resp agentwire.RenewResponse, presented bool, err error) {
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return resp, false, fmt.Errorf("renewal needs a client certificate")
	}
	leaf := state.PeerCertificates[0]
	idStr, ok := ca.AgentIDFromCert(leaf)
	if !ok {
		return resp, false, fmt.Errorf("certificate carries no agent identity")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return resp, false, fmt.Errorf("certificate carries an unreadable agent id")
	}
	agent, err := models.FindAgentByID(id)
	if err != nil {
		return resp, false, fmt.Errorf("unknown agent")
	}
	if !agent.Enabled {
		return resp, false, fmt.Errorf("agent is disabled")
	}
	presentedRoot := presentedIssuer(state)
	presented = h.recordPresented(id, presentedRoot)

	// The bundle first: an agent that has fallen behind a rollover needs the
	// new root before it is handed a certificate signed by it.
	bundle := h.store.Bundle()
	if req.Fingerprint != bundle.Fingerprint() {
		resp.CA = string(bundle.PEM())
	}

	issuer := h.store.Issuer()
	issuerPin := ca.Fingerprint(issuer.Certificate())
	if renewDue(leaf, time.Now()) || presentedRoot != issuerPin {
		certPEM, err := issuer.SignAgent([]byte(req.CSR), agent.ID.String(), h.certTTL)
		if err != nil {
			return agentwire.RenewResponse{}, presented, fmt.Errorf("signing agent certificate: %w", err)
		}
		h.recordIssued(agent.ID, certPEM, issuer)
		resp.Cert = string(certPEM)
		h.log.Info("renewed agent certificate",
			zap.String("agent", agent.ID.String()),
			zap.String("name", agent.Name))
	}
	if req.Version != "" && req.Version != agent.Version {
		_ = models.SetAgentVersion(agent.ID, req.Version)
	}
	return resp, presented, nil
}

// presentedIssuer returns the fingerprint of the root that verified the
// client certificate, empty when the handshake verified none.
func presentedIssuer(state tls.ConnectionState) string {
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return ""
	}
	chain := state.VerifiedChains[0]
	return ca.Fingerprint(chain[len(chain)-1])
}

// recordPresented stores the root an agent authenticated with and reports
// whether it changed. A failed write is logged; it only delays a rollover.
func (h *Hub) recordPresented(id uuid.UUID, issuer string) bool {
	if issuer == "" {
		return false
	}
	changed, err := models.RecordPresentedIssuer(id, issuer)
	if err != nil {
		h.log.Warn("recording presented agent certificate failed", zap.String("agent", id.String()), zap.Error(err))
		return false
	}
	return changed
}

// recordIssued stores what an agent was just handed, which is what a rollover
// counts to know who has moved onto the new root.
func (h *Hub) recordIssued(id uuid.UUID, certPEM []byte, issuer *ca.Authority) {
	leaf, err := ca.ParseCertPEM(certPEM)
	if err != nil {
		h.log.Warn("issued certificate did not parse back", zap.Error(err))
		return
	}
	if err := models.RecordAgentCert(id, ca.SerialString(leaf), ca.Fingerprint(issuer.Certificate()), leaf.NotAfter); err != nil {
		h.log.Warn("recording issued agent certificate failed", zap.Error(err))
	}
}

// handleConnect brings a mutually-authenticated agent into the plugin catalog
// and holds the connection until the session ends or the agent is dropped.
func (h *Hub) handleConnect(conn *tls.Conn) {
	// Clear the handshake deadline; a live session has no fixed lifetime.
	_ = conn.SetDeadline(time.Time{})

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		h.log.Debug("connect without a client certificate")
		_ = conn.Close()
		return
	}
	leaf := state.PeerCertificates[0]
	idStr, ok := ca.AgentIDFromCert(leaf)
	if !ok {
		_ = conn.Close()
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		_ = conn.Close()
		return
	}

	agent, err := models.FindAgentByID(id)
	if err != nil {
		h.log.Info("connect from an unknown agent", zap.String("agent", idStr))
		_ = conn.Close()
		return
	}
	if !agent.Enabled {
		h.log.Info("connect from a disabled agent", zap.String("agent", idStr))
		_ = conn.Close()
		return
	}
	if h.recordPresented(id, presentedIssuer(state)) {
		go func() {
			if err := h.Maintain(); err != nil {
				h.log.Warn("agent CA maintenance failed", zap.Error(err))
			}
		}()
	}

	// One session per agent: a reconnection replaces the old one rather than
	// stacking two sets of the same node types.
	lock := h.attachLock(id)
	lock.Lock()
	defer lock.Unlock()
	h.DropAgent(id)

	detach, done, err := h.mgr.AttachAgentConn(id.String(), agent.Name, conn)
	if err != nil {
		h.log.Warn("attaching agent failed", zap.String("agent", idStr), zap.Error(err))
		_ = conn.Close()
		return
	}

	_ = models.TouchAgentSeen(id, time.Now())

	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		detach()
		return
	}
	// The record was checked before an attach that takes seconds. A disable,
	// rename, or delete in that window found no session to drop, so it is read
	// again under the lock DropAgent takes: either this sees the change or
	// DropAgent sees the session.
	if current, err := models.FindAgentByID(id); err != nil || !current.Enabled || current.Name != agent.Name {
		h.mu.Unlock()
		h.log.Info("agent changed while connecting; closing session", zap.String("agent", idStr))
		detach()
		return
	}
	session := &liveSession{detach: detach, conn: conn, name: agent.Name}
	h.sessions[id] = session
	h.mu.Unlock()
	h.publish(AgentEvent{AgentID: id.String(), AgentName: agent.Name, State: agentConnected, At: time.Now()})

	// The certificate is checked only at the handshake, so a session would
	// otherwise outlive it. Ending it at expiry makes the agent reconnect with
	// its renewed certificate, or fail to if it never renewed.
	expire := time.AfterFunc(time.Until(leaf.NotAfter), func() {
		h.log.Info("agent certificate expired; closing session", zap.String("agent", idStr))
		h.dropSession(id, session)
	})

	// The transport signals its own end. When it closes, drop the session so a
	// later reconnect starts clean and the panel stops showing the agent live.
	go func() {
		<-done
		expire.Stop()
		h.dropSession(id, session)
	}()
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// uniqueAgentName returns a name not already taken, appending a short suffix on
// collision. An empty base falls back to "agent".
func uniqueAgentName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "agent"
	}
	if _, err := models.FindAgentByName(base); err != nil {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, err := models.FindAgentByName(candidate); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%s", base, uuid.NewString()[:8])
}

// serialFromCertPEM reads the serial from a signed leaf.
func serialFromCertPEM(certPEM []byte) (string, bool) {
	cert, err := ca.ParseCertPEM(certPEM)
	if err != nil {
		return "", false
	}
	return ca.SerialString(cert), true
}
