// Package credkey fetches the agent's credential-store master key from the
// server on a mutually authenticated connection. The key is the envelope key:
// the agent's on-disk credential store is sealed with it, so the store cannot
// be read — to enroll a credential or to unlock with one — without asking the
// server for the key first. It is held only in memory and zeroed after use.
package credkey

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"time"

	"github.com/grmrgecko/karakuri/agent/identity"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
)

// Fetch dials the server with the stored identity and returns the agent's
// credential-store master key. The connection is mutually authenticated, so the
// host reads which agent is asking from the certificate rather than being told;
// the same reason no request body is sent.
func Fetch(ctx context.Context, server, dir string) ([]byte, error) {
	_, cert, caPool, err := identity.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("loading the agent identity: %w", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   ca.ServerName,
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("dialing %s for the credential key: %w", server, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	if _, err := conn.Write([]byte{agentwire.ModeCredentialKey}); err != nil {
		return nil, fmt.Errorf("requesting the credential key: %w", err)
	}
	var resp agentwire.CredentialKeyResponse
	if err := agentwire.ReadJSON(conn, &resp); err != nil {
		return nil, fmt.Errorf("reading the credential key response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("the server declined the credential key: %s", resp.Error)
	}
	key, err := base64.StdEncoding.DecodeString(resp.Key)
	if err != nil {
		return nil, fmt.Errorf("decoding the credential key: %w", err)
	}
	return key, nil
}
