package agenthub_test

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
)

// TestRenewAndRollover walks the whole certificate lifecycle past enrollment:
// an agent asking when nothing is due, a CA rollover that leaves the listener
// on the outgoing root so nobody is locked out, the agent moving to the new
// root on its next renewal, and the promotion that follows once it has. This is
// the path a ten-year root eventually has to take without an operator present.
func TestRenewAndRollover(t *testing.T) {
	setupDB(t)
	clearAgents(t)

	registry := engine.NewRegistry()
	host := pluginhost.NewHost(registry)
	mgr := pluginhost.NewManager(host, zap.NewNop())

	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))
	defer hub.Stop()
	addr := hub.Addr()

	// Enroll, the way the agent command does.
	raw, _, err := models.NewEnrollmentToken("renewer", "", nil, 15*time.Minute)
	require.NoError(t, err)
	token, err := ca.EncodeJoin(hub.Bundle().PEM(), raw)
	require.NoError(t, err)
	join, err := ca.ParseJoin(token)
	require.NoError(t, err)

	keyPEM, err := ca.GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := ca.CreateCSR(keyPEM, "renewer-host")
	require.NoError(t, err)
	enrolled := enroll(t, addr, join, agentwire.EnrollRequest{
		Token: join.Secret, CSR: string(csrPEM), Hostname: "renewer-host", OS: "linux", Arch: "amd64",
	})
	require.Empty(t, enrolled.Error)

	trust, err := ca.AcceptBundle(join.CAPEM, []byte(enrolled.CA))
	require.NoError(t, err)
	oldTrust := trust
	clientCert, err := tls.X509KeyPair([]byte(enrolled.Cert), keyPEM)
	require.NoError(t, err)

	// A fresh certificate and a current bundle mean there is nothing to do,
	// which is the answer nearly every check gets.
	resp := renew(t, addr, clientCert, trust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: trust.Fingerprint(),
	})
	require.Empty(t, resp.Error)
	assert.Empty(t, resp.Cert, "a fresh certificate should not be reissued")
	assert.Empty(t, resp.CA, "an up-to-date bundle should not be resent")

	// An agent that reports a bundle the host does not recognise gets the
	// current one back.
	resp = renew(t, addr, clientCert, trust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: "stale",
	})
	require.Empty(t, resp.Error)
	assert.NotEmpty(t, resp.CA)
	assert.Empty(t, resp.Cert)

	// Start a rollover. The successor exists, but the listener keeps using the
	// outgoing root: an agent that has not renewed yet trusts nothing else, and
	// the renewal it needs happens over this very connection.
	require.NoError(t, hub.RotateCA())
	status := hub.CAStatus()
	assert.True(t, status.Rotating)
	assert.Equal(t, 2, status.Roots)

	resp = renew(t, addr, clientCert, oldTrust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: oldTrust.Fingerprint(),
	})
	require.Empty(t, resp.Error, "an agent on the old root must still be able to renew")
	require.NotEmpty(t, resp.CA, "the rollover must reach the agent")
	require.NotEmpty(t, resp.Cert, "a certificate from the outgoing root must be replaced")

	newTrust, err := ca.AcceptBundle(oldTrust.PEM(), []byte(resp.CA))
	require.NoError(t, err)
	require.Len(t, newTrust.Certs, 2, "both roots are trusted during the overlap")

	renewedID, err := ca.VerifyIssuedCert([]byte(resp.Cert), keyPEM, newTrust.Pool())
	require.NoError(t, err)
	assert.Equal(t, enrolled.AgentID, renewedID)

	// The new leaf really is from the successor: the old root alone cannot
	// verify it, which is what makes the overlap necessary.
	_, err = ca.VerifyIssuedCert([]byte(resp.Cert), keyPEM, oldTrust.Pool())
	assert.Error(t, err)

	renewedCert, err := tls.X509KeyPair([]byte(resp.Cert), keyPEM)
	require.NoError(t, err)

	// Issuing is not proof the agent stored anything: the reply can be lost or
	// the disk write can fail. Promotion waits, and the next check from the
	// old certificate is issued a successor certificate again.
	require.NoError(t, hub.Maintain())
	assert.True(t, hub.CAStatus().Rotating, "an issued but never presented certificate must not promote the successor")
	resp = renew(t, addr, clientCert, newTrust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: newTrust.Fingerprint(),
	})
	require.Empty(t, resp.Error)
	assert.NotEmpty(t, resp.Cert, "an agent still presenting the outgoing root must be issued again")

	// Presenting the successor certificate is the acknowledgement. The renewal
	// handler runs maintenance, which promotes the successor and reissues the
	// listener's own leaf under it.
	resp = renew(t, addr, renewedCert, newTrust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: newTrust.Fingerprint(),
	})
	require.Empty(t, resp.Error)
	assert.Empty(t, resp.Cert, "a presented successor certificate is current")
	assert.Eventually(t, func() bool { return !hub.CAStatus().Rotating }, 2*time.Second, 10*time.Millisecond,
		"promotion should follow the last agent presenting the successor")

	// The renewed agent still works, verifying the listener with the bundle it
	// was given.
	resp = renew(t, addr, renewedCert, newTrust, agentwire.RenewRequest{
		CSR: string(csrPEM), Fingerprint: newTrust.Fingerprint(),
	})
	require.Empty(t, resp.Error)

	// And the promoted leaf no longer verifies against the outgoing root alone,
	// which is why promotion waits for the agents.
	_, err = tls.Dial("tcp", addr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{renewedCert},
		RootCAs:      oldTrust.Pool(),
		ServerName:   ca.ServerName,
	})
	assert.Error(t, err)
}

// TestSessionEndsAtCertificateExpiry pins that a live session does not outlive
// its certificate. TLS checks expiry only at the handshake, so before the
// session timer an agent that never renewed stayed connected indefinitely.
// With a 3-second certificate the session must close and the reconnect must
// fail its handshake.
func TestSessionEndsAtCertificateExpiry(t *testing.T) {
	setupDB(t)
	clearAgents(t)

	registry := engine.NewRegistry()
	host := pluginhost.NewHost(registry)
	mgr := pluginhost.NewManager(host, zap.NewNop())
	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 3*time.Second)
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))
	defer hub.Stop()
	addr := hub.Addr()

	raw, _, err := models.NewEnrollmentToken("expiring", "", nil, 15*time.Minute)
	require.NoError(t, err)
	token, err := ca.EncodeJoin(hub.Bundle().PEM(), raw)
	require.NoError(t, err)
	join, err := ca.ParseJoin(token)
	require.NoError(t, err)
	keyPEM, err := ca.GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := ca.CreateCSR(keyPEM, "expiring-host")
	require.NoError(t, err)
	enrolled := enroll(t, addr, join, agentwire.EnrollRequest{
		Token: join.Secret, CSR: string(csrPEM), Hostname: "expiring-host", OS: "linux", Arch: "amd64",
	})
	require.Empty(t, enrolled.Error)
	clientCert, err := tls.X509KeyPair([]byte(enrolled.Cert), keyPEM)
	require.NoError(t, err)
	id := mustUUID(t, enrolled.AgentID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sdk.ServeAgent(ctx, sdk.AgentTransport{
			ServerAddr: addr,
			TLSConfig: func() (*tls.Config, error) {
				return &tls.Config{
					MinVersion:   tls.VersionTLS13,
					Certificates: []tls.Certificate{clientCert},
					RootCAs:      join.Pool(),
					ServerName:   ca.ServerName,
				}, nil
			},
			Logger: zap.NewNop(),
		}, testAgentPlugin{})
	}()

	require.Eventually(t, func() bool { return hub.IsConnected(id) }, 3*time.Second, 20*time.Millisecond,
		"the agent must connect while its certificate is valid")
	require.Eventually(t, func() bool { return !hub.IsConnected(id) }, 5*time.Second, 50*time.Millisecond,
		"the session must close when the certificate expires")
	time.Sleep(time.Second)
	assert.False(t, hub.IsConnected(id), "an expired certificate must not reconnect")
}

// renew runs the client half of a renewal round trip.
func renew(t *testing.T, addr string, cert tls.Certificate, trust ca.Bundle, req agentwire.RenewRequest) agentwire.RenewResponse {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      trust.Pool(),
		ServerName:   ca.ServerName,
	})
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte{agentwire.ModeRenew})
	require.NoError(t, err)
	require.NoError(t, agentwire.WriteJSON(conn, req))

	var resp agentwire.RenewResponse
	require.NoError(t, agentwire.ReadJSON(conn, &resp))
	return resp
}

// clearAgents empties the agent table. The package shares one in-memory
// database, and a rollover waits on every enabled agent, so records another
// test enrolled and abandoned would hold promotion forever.
func clearAgents(t *testing.T) {
	t.Helper()
	agents, err := models.ListAgents()
	require.NoError(t, err)
	for _, a := range agents {
		require.NoError(t, models.DeleteAgent(a.ID))
	}
}
