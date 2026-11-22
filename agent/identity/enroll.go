package identity

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
)

// Enroll registers this host with a karakuri server using a join token. It
// generates a key locally, sends a CSR, and stores the signed certificate,
// returning the identity the server named. The token carries the CA the server
// is verified by, so nothing has to be installed on this host first.
func Enroll(dir, server, token, name string) (Identity, error) {
	join, err := ca.ParseJoin(token)
	if err != nil {
		return Identity{}, err
	}
	if IsEnrolled(dir) {
		return Identity{}, fmt.Errorf("already enrolled in %s; delete it to re-enroll", dir)
	}

	// Generate the key once and keep it, so a retried enrollment reuses it.
	keyPEM, err := ca.GenerateAgentKey()
	if err != nil {
		return Identity{}, err
	}
	if err := writeKey(dir, keyPEM); err != nil {
		return Identity{}, err
	}

	hostname, _ := os.Hostname()
	csrPEM, err := ca.CreateCSR(keyPEM, hostname)
	if err != nil {
		return Identity{}, err
	}

	resp, err := enrollExchange(server, join, agentwire.EnrollRequest{
		Token:    join.Secret,
		CSR:      string(csrPEM),
		Hostname: hostname,
		Name:     name,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Version:  version.Version,
	})
	if err != nil {
		return Identity{}, err
	}
	if resp.Error != "" {
		return Identity{}, fmt.Errorf("enrollment refused: %s", resp.Error)
	}

	// The reply may extend the trust bundle — a CA rollover in progress means
	// the roots to keep are not only the one in the token — but it may never
	// replace it: AcceptBundle refuses anything that does not still contain a
	// root the token carried.
	trustPEM := join.CAPEM
	if resp.CA != "" {
		bundle, err := ca.AcceptBundle(join.CAPEM, []byte(resp.CA))
		if err != nil {
			return Identity{}, err
		}
		trustPEM = bundle.PEM()
	}

	// Check what came back before storing it: the certificate has to chain to a
	// root now trusted and be for the key generated above.
	trust, err := ca.ParseBundle(trustPEM)
	if err != nil {
		return Identity{}, err
	}
	agentID, err := ca.VerifyIssuedCert([]byte(resp.Cert), keyPEM, trust.Pool())
	if err != nil {
		return Identity{}, err
	}
	if agentID != resp.AgentID {
		return Identity{}, fmt.Errorf("server named agent %q but signed a certificate for %q", resp.AgentID, agentID)
	}

	id := Identity{AgentID: resp.AgentID, AgentName: resp.AgentName, Server: server}
	if err := Save(dir, id, keyPEM, []byte(resp.Cert), trustPEM); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// enrollExchange dials the host, verifies it against the token's CA, and
// completes the enrollment round trip. Nothing here depends on the address
// dialed: the server leaf is issued to ca.ServerName and the CA is the whole
// check.
func enrollExchange(server string, join ca.JoinToken, req agentwire.EnrollRequest) (agentwire.EnrollResponse, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    join.Pool(),
		ServerName: ca.ServerName,
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, tlsCfg)
	if err != nil {
		return agentwire.EnrollResponse{}, fmt.Errorf("dialing %s: %w", server, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if _, err := conn.Write([]byte{agentwire.ModeEnroll}); err != nil {
		return agentwire.EnrollResponse{}, err
	}
	if err := agentwire.WriteJSON(conn, req); err != nil {
		return agentwire.EnrollResponse{}, err
	}
	var resp agentwire.EnrollResponse
	if err := agentwire.ReadJSON(conn, &resp); err != nil {
		return agentwire.EnrollResponse{}, fmt.Errorf("reading enroll response: %w", err)
	}
	return resp, nil
}
