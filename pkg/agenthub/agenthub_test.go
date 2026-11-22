package agenthub_test

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
)

// TestEnrollThenConnect drives the whole transport: a fresh host generates a
// CA, mints a token, an in-process agent enrolls over it and then dials back
// with its certificate, and the host ends up with the agent's node type
// registered under its namespace. This is the path the network, TLS, yamux, and
// bidirectional gRPC all have to get right.
func TestEnrollThenConnect(t *testing.T) {
	setupDB(t)

	registry := engine.NewRegistry()
	host := pluginhost.NewHost(registry)
	mgr := pluginhost.NewManager(host, zap.NewNop())

	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))
	defer hub.Stop()

	addr := hub.Addr()

	// Mint a join token the way the operator's panel would.
	raw, _, err := models.NewEnrollmentToken("test-agent", "", nil, 15*time.Minute)
	require.NoError(t, err)
	token, err := ca.EncodeJoin(hub.Bundle().PEM(), raw)
	require.NoError(t, err)
	join, err := ca.ParseJoin(token)
	require.NoError(t, err)

	// Enroll: generate a key locally, send a CSR, get a signed cert back.
	keyPEM, err := ca.GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := ca.CreateCSR(keyPEM, "test-host")
	require.NoError(t, err)

	resp := enroll(t, addr, join, agentwire.EnrollRequest{
		Token:    join.Secret,
		CSR:      string(csrPEM),
		Hostname: "test-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "test",
	})
	require.Empty(t, resp.Error)
	require.NotEmpty(t, resp.Cert)
	agentID := resp.AgentID

	// The signed certificate is checked against the token's CA before it is
	// used, exactly as the agent command does.
	signedID, err := ca.VerifyIssuedCert([]byte(resp.Cert), keyPEM, join.Pool())
	require.NoError(t, err)
	require.Equal(t, resp.AgentID, signedID)

	// Connect: serve the agent with its certificate. It registers one node.
	clientCert, err := tls.X509KeyPair([]byte(resp.Cert), keyPEM)
	require.NoError(t, err)

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

	// The node lands in the host registry under the agent's namespace, with its
	// palette group set to the agent's name.
	want := pluginhost.AgentTypePrefix(agentID) + "echo"
	var nt engine.NodeType
	require.Eventually(t, func() bool {
		got, ok := registry.Get(want)
		if ok {
			nt = got
		}
		return ok
	}, 5*time.Second, 20*time.Millisecond, "agent node should register after connect")

	assert.Equal(t, "test-agent", nt.Meta().Group)
	assert.Equal(t, flow.InvocationStateUnknown, nt.Meta().InvocationState,
		"agent metadata without a store-use declaration must require the conservative invocation identity")
	assert.True(t, hub.IsConnected(mustUUID(t, agentID)))
}

// enroll runs the client half of the enrollment exchange, verifying the host
// against the token's CA the way the real agent does. The dialed address is
// 127.0.0.1 and the certificate names none, which is the point.
func enroll(t *testing.T, addr string, join ca.JoinToken, req agentwire.EnrollRequest) agentwire.EnrollResponse {
	t.Helper()
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    join.Pool(),
		ServerName: ca.ServerName,
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte{agentwire.ModeEnroll})
	require.NoError(t, err)
	require.NoError(t, agentwire.WriteJSON(conn, req))
	var resp agentwire.EnrollResponse
	require.NoError(t, agentwire.ReadJSON(conn, &resp))
	return resp
}

// setupDB isolates the database and certificate files from installed state.
func setupDB(t *testing.T) {
	t.Helper()
	// The certificate store must not resolve to a real user or system install.
	dataPath := filepath.Join(resources.BinDir(), "var")
	require.NoError(t, os.Mkdir(dataPath, 0700))
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dataPath)) })
	require.NoError(t, config.Load())
	// The CA key is encrypted at rest, so the test needs a real 32-byte key.
	config.C.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}))
	require.NoError(t, migrate.AutoMigrate())
}

// mustUUID parses a UUID or fails the test.
func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	v, err := uuid.Parse(s)
	require.NoError(t, err)
	return v
}

// testAgentPlugin registers a single echo node, standing in for the real
// agent's script node.
type testAgentPlugin struct{}

func (testAgentPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Name: "agent", Version: "test"}
}

func (testAgentPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(echoNode{})
	return nil
}

type echoNode struct{}

func (echoNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:  "echo",
		Label: "Echo",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny},
		},
	}
}

func (echoNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return map[string][]*engine.Msg{"out": {inputs["in"]}}, nil
}
