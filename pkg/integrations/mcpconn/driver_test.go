package mcpconn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMain doubles the test binary as a stdio MCP server: re-executed with
// MCPCONN_STDIO_SERVER=1 it serves the same echo tool over its pipes, which is
// how the stdio transport is exercised against a real subprocess boundary.
func TestMain(m *testing.M) {
	if os.Getenv("MCPCONN_STDIO_SERVER") == "1" {
		server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
			func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
				return nil, map[string]any{"echo": in.Text, "greeting": os.Getenv("MCPCONN_GREETING")}, nil
			})
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// echoInput is the test tool's argument shape; the SDK infers its schema.
type echoInput struct {
	Text string `json:"text"`
}

// testServer serves a real MCP server over streamable HTTP with one echo tool
// and one always-failing tool, wrapped to record the auth header.
func testServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return nil, map[string]any{"echo": in.Text}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boom", Description: "always fails"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "it broke"}},
			}, nil, nil
		})
	// A screenshot-shaped tool: an image block plus a text metadata block, the
	// exact shape computer-use-linux's screenshot returns.
	mcp.AddTool(server, &mcp.Tool{Name: "shot", Description: "returns an image"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.ImageContent{MIMEType: "image/png", Data: []byte("PNG!")},
					&mcp.TextContent{Text: `{"width":1720}`},
				},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDriverAgainstRealServer drives the driver against the SDK's own
// streamable HTTP server: the handshake completes, the tool list arrives with
// a usable schema map, arguments cross the wire, the bearer token rides every
// request, and an MCP-level tool failure comes back as IsError rather than an
// error — the distinction the agent's tool loop is built on.
func TestDriverAgainstRealServer(t *testing.T) {
	var gotAuth string
	srv := testServer(t, &gotAuth)

	d, err := New(integrations.ConnModel{
		ID:     uuid.New(),
		Kind:   Kind,
		Config: []byte(`{"url":"` + srv.URL + `","bearer_token":"tok-123"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	tools, err := drv.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 3)
	assert.Equal(t, "Bearer tok-123", gotAuth, "the configured token must ride every request")
	byName := map[string]Tool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	echo := byName["echo"]
	require.NotNil(t, echo.InputSchema, "the server's inferred schema must arrive as plain data")
	assert.Equal(t, "object", echo.InputSchema["type"], "an SDK-inferred schema is a JSON Schema object")

	res, err := drv.CallTool(context.Background(), "echo", map[string]any{"text": "hi"})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, map[string]any{"echo": "hi"}, res.Value,
		"structured content is preferred over the text rendering")

	res, err = drv.CallTool(context.Background(), "boom", map[string]any{"text": "x"})
	require.NoError(t, err, "a tool-level failure is a result, not a transport error")
	assert.True(t, res.IsError)
	assert.Equal(t, "it broke", res.Value, "the failure text is the observation the model gets")

	res, err = drv.CallTool(context.Background(), "shot", map[string]any{"text": "x"})
	require.NoError(t, err)
	require.Len(t, res.Images, 1, "the image block must survive beside the text metadata")
	assert.Equal(t, "image/png", res.Images[0].MIME)
	assert.Equal(t, []byte("PNG!"), res.Images[0].Data, "image data arrives as raw bytes, not base64")
	assert.Equal(t, `{"width":1720}`, res.Value, "the text metadata block is still the payload")
}

// TestDriverOverStdio spawns the test binary as a real MCP server subprocess
// (see TestMain) and drives it over the stdio transport: the handshake and a
// tool round trip cross real pipes, and the configured env entries reach the
// child — the plumbing a stdio server's API keys ride.
func TestDriverOverStdio(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	d, err := New(integrations.ConnModel{
		ID:   uuid.New(),
		Kind: Kind,
		Config: []byte(`{"transport":"stdio","command":"` + exe + `",` +
			`"env":{"MCPCONN_STDIO_SERVER":"1","MCPCONN_GREETING":"hi from env"}}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	tools, err := drv.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)

	res, err := drv.CallTool(context.Background(), "echo", map[string]any{"text": "over pipes"})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, map[string]any{"echo": "over pipes", "greeting": "hi from env"}, res.Value,
		"arguments and configured env entries must both reach the subprocess")
}

// TestDriverOverSSEKeepsStream pins the SSE transport's context handling in
// go-sdk v1.7.0: SSEClientTransport.Connect opens the long-lived event stream
// on the context it is given, so a stream opened on the dial-timeout context
// died the moment the dial returned and every later call failed. Two calls
// after the session is held must both succeed.
func TestDriverOverSSEKeepsStream(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return nil, map[string]any{"echo": in.Text}, nil
		})
	srv := httptest.NewServer(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)

	d, err := New(integrations.ConnModel{
		ID:     uuid.New(),
		Kind:   Kind,
		Config: []byte(`{"transport":"sse","url":"` + srv.URL + `"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	tools, err := drv.Tools(context.Background())
	require.NoError(t, err, "the first call after the dial must reach the server")
	require.Len(t, tools, 1)

	res, err := drv.CallTool(context.Background(), "echo", map[string]any{"text": "still open"})
	require.NoError(t, err, "the event stream must outlive the dial that opened it")
	assert.Equal(t, map[string]any{"echo": "still open"}, res.Value)
}

// TestRefusedCallKeepsSession locks that a call the server answers with a
// JSON-RPC error, such as a tool it does not have, fails without retiring the
// session. The refusal was handled as a dead transport: the session closed,
// which ends a stdio server's process and whatever state it held, and the
// connection reported an error although the server had just answered.
func TestRefusedCallKeepsSession(t *testing.T) {
	var gotAuth string
	srv := testServer(t, &gotAuth)
	d, err := New(integrations.ConnModel{
		ID: uuid.New(), Kind: Kind, Config: []byte(`{"url":"` + srv.URL + `"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	_, err = drv.Tools(context.Background())
	require.NoError(t, err)
	held, err := drv.session(context.Background())
	require.NoError(t, err)

	_, err = drv.CallTool(context.Background(), "no-such-tool", map[string]any{})
	require.Error(t, err, "the server's refusal is still the call's error")

	after, err := drv.session(context.Background())
	require.NoError(t, err)
	assert.True(t, held == after, "a server that answered keeps its session")
	assert.Equal(t, integrations.StateConnected, drv.Status().State, "a refused call is not a connection failure")
}

// TestCanceledToolListingKeepsStatus locks that a tool listing whose caller
// went away, such as a canceled flow run, leaves the connection's status
// alone. Nothing probes an MCP connection back to connected, so a healthy
// server would show as failed until something used it again.
func TestCanceledToolListingKeepsStatus(t *testing.T) {
	var gotAuth string
	srv := testServer(t, &gotAuth)
	d, err := New(integrations.ConnModel{
		ID: uuid.New(), Kind: Kind, Config: []byte(`{"url":"` + srv.URL + `"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	_, err = drv.Tools(context.Background())
	require.NoError(t, err)
	require.Equal(t, integrations.StateConnected, drv.Status().State, "a listing that worked reports a connected server")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = drv.Tools(ctx)
	require.Error(t, err, "the canceled listing fails for its caller")

	status := drv.Status()
	assert.Equal(t, integrations.StateConnected, status.State,
		"a caller's cancel must not mark the connection failed (error: %q)", status.Error)
}

// TestCanceledCallKeepsSession locks that a call whose caller went away, such
// as a canceled flow run, fails for that caller alone. The cancel was handled
// as a dead transport: the shared session closed under every other run using
// it, and a stdio server lost its process and the state it held. go-sdk
// v1.7.0 answers a canceled call by telling the server to stop that one
// request, and the session carries the next call.
func TestCanceledCallKeepsSession(t *testing.T) {
	started := make(chan struct{}, 1)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "wait", Description: "blocks until canceled"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return nil, map[string]any{"echo": in.Text}, nil
		})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)

	d, err := New(integrations.ConnModel{
		ID: uuid.New(), Kind: Kind, Config: []byte(`{"url":"` + srv.URL + `"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	drv := d.(*Driver)
	require.NoError(t, drv.Start(context.Background()))
	defer drv.Stop()

	held, err := drv.session(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err = drv.CallTool(ctx, "wait", map[string]any{"text": "x"})
	require.ErrorIs(t, err, context.Canceled, "the canceled call fails for its caller")

	after, err := drv.session(context.Background())
	require.NoError(t, err)
	assert.True(t, held == after, "a caller's cancel must not retire the session other runs share")

	res, err := drv.CallTool(context.Background(), "echo", map[string]any{"text": "next"})
	require.NoError(t, err, "the session must carry the next call")
	assert.Equal(t, map[string]any{"echo": "next"}, res.Value)
}
