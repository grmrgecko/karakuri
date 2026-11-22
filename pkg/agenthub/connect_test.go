package agenthub_test

import (
	"context"
	"crypto/tls"
	"sync"
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

// TestConnectRefusesAgentDisabledWhileAttaching locks a disable to cutting the
// agent off even when it lands while that agent is connecting. The connect
// handler checked the record before an attach that takes seconds, and the
// operator's drop found no session yet, so the agent stayed attached with its
// nodes in the palette until it next reconnected.
func TestConnectRefusesAgentDisabledWhileAttaching(t *testing.T) {
	setupDB(t)

	registry := engine.NewRegistry()
	mgr := pluginhost.NewManager(pluginhost.NewHost(registry), zap.NewNop())
	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 24*time.Hour)
	require.NoError(t, err, "the hub must build against the test database")
	require.NoError(t, hub.Start(context.Background()), "the agent listener must start")
	defer hub.Stop()

	raw, _, err := models.NewEnrollmentToken("racer", "", nil, 15*time.Minute)
	require.NoError(t, err, "the test needs a join token")
	token, err := ca.EncodeJoin(hub.Bundle().PEM(), raw)
	require.NoError(t, err, "the join token must encode")
	join, err := ca.ParseJoin(token)
	require.NoError(t, err, "the join token must parse back")
	keyPEM, err := ca.GenerateAgentKey()
	require.NoError(t, err, "the agent key must generate")
	csrPEM, err := ca.CreateCSR(keyPEM, "racer-host")
	require.NoError(t, err, "the CSR must build")
	resp := enroll(t, hub.Addr(), join, agentwire.EnrollRequest{
		Token: join.Secret, CSR: string(csrPEM), Hostname: "racer-host", OS: "linux", Arch: "amd64",
	})
	require.Empty(t, resp.Error, "enrollment must succeed before a connect can race anything")
	id := mustUUID(t, resp.AgentID)
	clientCert, err := tls.X509KeyPair([]byte(resp.Cert), keyPEM)
	require.NoError(t, err, "the issued certificate must pair with the agent key")

	// Holding the connect lock parks the handler after its enabled check and
	// before the attach, the window a disable has to survive.
	lock := hub.AttachLock(id)
	lock.Lock()
	var release sync.Once
	defer release.Do(lock.Unlock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sdk.ServeAgent(ctx, sdk.AgentTransport{
			ServerAddr: hub.Addr(),
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

	// The handler records the presented root right after the enabled check, so
	// seeing it stored means the check has already passed.
	require.Eventually(t, func() bool {
		agent, err := models.FindAgentByID(id)
		return err == nil && agent.PresentedIssuer != ""
	}, 5*time.Second, 10*time.Millisecond, "the connect must reach the attach lock")

	require.NoError(t, models.UpdateAgentDetails(id, "racer", "", false), "the operator's disable must store")
	hub.DropAgent(id)
	release.Do(lock.Unlock)

	assert.Never(t, func() bool { return hub.IsConnected(id) },
		2*time.Second, 20*time.Millisecond, "a disabled agent must not end up holding a session")
	_, registered := registry.Get(pluginhost.AgentTypePrefix(resp.AgentID) + "echo")
	assert.False(t, registered, "the refused session's node types must be withdrawn")
}
