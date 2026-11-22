package pluginhost

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// tokenDriver stands in for an oauth2 connection's driver.
type tokenDriver struct {
	token string
	err   error
}

func (d tokenDriver) AccessToken(context.Context) (string, error) { return d.token, d.err }

// TestConnectionAccessToken covers the token callback: it returns a token from
// a driver that issues them and refuses everything else with a reason a
// plugin can surface.
func TestConnectionAccessToken(t *testing.T) {
	oauth := uuid.New()
	expired := uuid.New()
	plain := uuid.New()
	SetConnectionLookup(func(id uuid.UUID) (any, bool) {
		switch id {
		case oauth:
			return tokenDriver{token: "ya29.test"}, true
		case expired:
			return tokenDriver{err: errors.New("sign in again")}, true
		case plain:
			return struct{}{}, true
		}
		return nil, false
	})
	t.Cleanup(func() { SetConnectionLookup(nil) })
	p := newPluginHost(newHostService(), "calendar", zap.NewNop())

	cases := []struct {
		name, id, token, err string
	}{
		{"oauth2 connection", oauth.String(), "ya29.test", ""},
		{"signed out", expired.String(), "", "sign in again"},
		{"not a token source", plain.String(), "", "connection does not issue access tokens; use an oauth2 connection"},
		{"not running", uuid.NewString(), "", "connection is not running; is it enabled?"},
		{"bad ID", "nope", "", `invalid connection ID "nope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := p.ConnectionAccessToken(context.Background(), &pb.ConnectionTokenRequest{ConnId: tc.id})
			require.NoError(t, err, "refusals travel in the response, not as transport errors")
			assert.Equal(t, tc.token, resp.Token)
			assert.Equal(t, tc.err, resp.Error)
		})
	}
}

// TestAgentCannotReadConnectionTokens pins that an enrolled agent is refused
// the access token callback. An agent is another machine holding a
// certificate, served only run-scoped callbacks and its own settings; the
// callback was reachable through its channel and handed any host oauth2
// connection's bearer token to whichever agent named the connection.
func TestAgentCannotReadConnectionTokens(t *testing.T) {
	oauth := uuid.New()
	SetConnectionLookup(func(id uuid.UUID) (any, bool) {
		return tokenDriver{token: "ya29.test"}, id == oauth
	})
	t.Cleanup(func() { SetConnectionLookup(nil) })
	agent := agentHost{newPluginHost(newHostService(), AgentPluginName("a1"), zap.NewNop())}

	resp, err := agent.ConnectionAccessToken(context.Background(), &pb.ConnectionTokenRequest{ConnId: oauth.String()})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "an agent must be refused, not answered")
	assert.Empty(t, resp.GetToken(), "no token may reach an agent")
}
