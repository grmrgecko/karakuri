package agenthub_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
)

// TestServerCertificateSurvivesRestart proves the listener's own leaf is kept
// rather than minted per process. A server that reissued on every start would
// look fine in a test and still expire under an operator who leaves it running
// for months, which is the case this whole path exists for.
func TestServerCertificateSurvivesRestart(t *testing.T) {
	setupDB(t)

	first := startHub(t)
	before := serverSerial(t, first)
	first.Stop()

	second := startHub(t)
	defer second.Stop()
	after := serverSerial(t, second)

	assert.Equal(t, before, after, "a restart should present the stored certificate")
	assert.False(t, second.CAStatus().ServerCert.IsZero(), "the panel needs the leaf's expiry")
}

// startHub opens a hub on an ephemeral port.
func startHub(t *testing.T) *agenthub.Hub {
	t.Helper()
	registry := engine.NewRegistry()
	host := pluginhost.NewHost(registry)
	mgr := pluginhost.NewManager(host, zap.NewNop())
	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))
	return hub
}

// serverSerial reads the serial of the leaf the listener presents.
func serverSerial(t *testing.T, hub *agenthub.Hub) string {
	t.Helper()
	conn, err := tls.Dial("tcp", hub.Addr(), &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    hub.Bundle().Pool(),
		ServerName: ca.ServerName,
	})
	require.NoError(t, err)
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	require.NotEmpty(t, certs)
	return ca.SerialString(certs[0])
}
