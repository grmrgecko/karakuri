//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Microsoft/go-winio"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResidentProtocol checks framing, malformed versions, and bounded output
// without launching desktop actions from a test process.
func TestResidentProtocol(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request residentRequest
		code    int
	}{
		{name: "old protocol", request: residentRequest{Version: 0, Args: []string{"version"}}, code: 2},
		{name: "management refused", request: residentRequest{Version: residentProtocol, Args: []string{"resident", "install"}}, code: 2},
		{name: "successful command", request: residentRequest{Version: residentProtocol, Args: []string{"-test.run=^$"}}, code: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			go func() { defer close(done); answerResident(context.Background(), server, nil) }()
			require.NoError(t, json.NewEncoder(client).Encode(tc.request))
			var response residentResponse
			require.NoError(t, json.NewDecoder(client).Decode(&response))
			_, err := client.Write([]byte{1})
			require.NoError(t, err)
			assert.Equal(t, residentProtocol, response.Version)
			assert.Equal(t, tc.code, response.Code)
			if tc.code == 0 {
				assert.Contains(t, response.Stdout, "PASS")
			} else {
				assert.Contains(t, response.Stderr, "invalid resident request")
			}
			<-done
		})
	}
	output := &limitedOutput{}
	n, err := io.Copy(output, strings.NewReader(strings.Repeat("x", residentOutputLimit+100)))
	require.NoError(t, err)
	assert.EqualValues(t, residentOutputLimit+100, n)
	assert.Equal(t, residentOutputLimit, output.Len())
	assert.Contains(t, output.String(), "output truncated")
}

// TestResidentWindowsPipe pins named-pipe transport on Windows 7 as well as
// current Windows, without requiring a signed-in desktop.
func TestResidentWindowsPipe(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\karakuri-auto-test-%d`, os.Getpid())
	listener, err := listenResidentPipe(path, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			answerResident(ctx, connection, nil)
		}
		done <- err
	}()
	client, err := winio.DialPipeContext(ctx, path)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, json.NewEncoder(client).Encode(residentRequest{Version: residentProtocol, Args: []string{"-test.run=^$"}}))
	var response residentResponse
	require.NoError(t, json.NewDecoder(client).Decode(&response))
	_, err = client.Write([]byte{1})
	require.NoError(t, err)
	require.NoError(t, <-done)
	assert.Equal(t, 0, response.Code)
	assert.Contains(t, response.Stdout, "PASS")
}
