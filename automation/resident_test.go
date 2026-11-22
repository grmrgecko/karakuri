package automation

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResidentConnections exercises the public protocol over independent
// connections. Closing one command must leave the desktop available to the next.
func TestResidentConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := NewSessionServer(&Session{
		Clipboard: fakeClipboard{text: "resident clipboard"},
		env:       Environment{OS: "fixture", Backends: map[string]string{"clipboard": "fixture"}},
	})
	for i := 0; i < 2; i++ {
		clientConn, serverConn := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- server.Serve(ctx, serverConn) }()
		client, err := NewRemoteSession(ctx, clientConn)
		require.NoError(t, err)
		assert.Equal(t, "fixture", client.Describe().OS)
		text, err := client.Clipboard.Read(ctx, autoapi.SelectionClipboard)
		require.NoError(t, err)
		assert.Equal(t, "resident clipboard", text)
		require.NoError(t, client.Close())
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("resident retained a disconnected client")
		}
	}
}
