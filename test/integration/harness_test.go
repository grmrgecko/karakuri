//go:build karakuri_integration

// Package integration drives a real karakuri instance over its HTTP API.
//
// The suite builds the binary, boots it against a throwaway SQLite database on
// a private port, and asserts on what an operator would see. It is the safety
// net the unit tier deliberately does not duplicate: nothing here is faked, so
// a break in the wiring between the router, the engine, the sandbox, and the
// database surfaces here rather than nowhere.
//
// Build-tagged so `go test ./...` stays dependency-free. Run it with
// `make test-integration`.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Fixture credentials for the throwaway instance. It listens on loopback with
// its own database and is destroyed with the test run, so the values are
// deliberately fixed rather than secret.
const (
	adminUser     = "integration"
	adminEmail    = "integration@example.invalid"
	adminPassword = "integration-password"
)

// srv is the instance shared by every test in the package. Booting the server
// costs a Go build plus a migration pass, and the tests each create their own
// flows, so one instance serves them all.
var srv *instance

// instance is one running karakuri server together with the throwaway
// directory holding its binary, config, and database.
type instance struct {
	dir     string
	baseURL string
	token   string
	logPath string
	client  *http.Client
	cmd     *exec.Cmd
}

// TestMain boots the instance before the suite and tears it down after,
// whatever the tests did.
func TestMain(m *testing.M) {
	code := func() int {
		var err error
		srv, err = boot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "integration harness failed to start: %v\n", err)
			return 1
		}
		defer srv.stop()
		return m.Run()
	}()
	os.Exit(code)
}

// boot builds the binary, lays out an isolated resource tree around it, seeds
// an admin user, and starts the server.
//
// Isolation comes from where the binary sits rather than from environment
// overrides: karakuri resolves its config, data, plugin, and theme directories
// relative to the running executable and only falls back to /etc and
// /usr/share when the local ones are absent. Building into a temp directory
// and creating all four there means the instance cannot read an operator's
// real config or load their installed plugins.
func boot() (*instance, error) {
	dir, err := os.MkdirTemp("", "karakuri-integration-")
	if err != nil {
		return nil, fmt.Errorf("cannot create the instance directory: %w", err)
	}
	in := &instance{
		dir:     dir,
		logPath: filepath.Join(dir, "serve.log"),
		client:  &http.Client{Timeout: 60 * time.Second},
	}

	root, err := repoRoot()
	if err != nil {
		in.stop()
		return nil, err
	}

	// Build the binary under test into the instance directory.
	bin := filepath.Join(dir, "karakuri")
	build := exec.Command("go", "build", "-o", bin, "./cmd/karakuri")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot build karakuri: %w\n%s", err, out)
	}

	// Empty plugin and theme directories short-circuit the resource search, so
	// the instance never picks up a system install.
	for _, sub := range []string{"etc", "var", "plugins", "themes"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			in.stop()
			return nil, fmt.Errorf("cannot create %s: %w", sub, err)
		}
	}

	// Generate the config the instance runs on, next to its own binary.
	cfg, err := exec.Command(bin, "config", "gen", "--env=prod").Output()
	if err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot generate a config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "config.yaml"), cfg, 0o600); err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot write the config: %w", err)
	}

	port, err := freePort()
	if err != nil {
		in.stop()
		return nil, err
	}
	in.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	env := append(os.Environ(),
		"KARAKURI_SERVER_HOST=127.0.0.1",
		"KARAKURI_AGENTS_LISTEN=127.0.0.1:0",
		fmt.Sprintf("KARAKURI_SERVER_PORT=%d", port),
	)

	// Seed the admin account the tests authenticate as.
	create := exec.Command(bin, "user", "create", adminUser, adminEmail, adminPassword, "--admin")
	create.Env = env
	if out, err := create.CombinedOutput(); err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot create the admin user: %w\n%s", err, out)
	}

	log, err := os.Create(in.logPath)
	if err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot open the server log: %w", err)
	}
	defer func() { _ = log.Close() }()

	in.cmd = exec.Command(bin, "serve")
	in.cmd.Env = env
	in.cmd.Stdout = log
	in.cmd.Stderr = log
	if err := in.cmd.Start(); err != nil {
		in.stop()
		return nil, fmt.Errorf("cannot start the server: %w", err)
	}

	if err := in.waitHealthy(); err != nil {
		in.stop()
		return nil, err
	}
	if err := in.login(); err != nil {
		in.stop()
		return nil, err
	}
	return in, nil
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot resolve the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// freePort reserves a loopback port and releases it for the server to bind.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("cannot reserve a port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitHealthy polls /health until the server answers, failing early if the
// process exited instead.
func (in *instance) waitHealthy() error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if in.cmd.ProcessState != nil {
			return fmt.Errorf("the server exited during startup\n%s", in.tail())
		}
		resp, err := in.client.Get(in.baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the server never became healthy\n%s", in.tail())
}

// login exchanges the fixture credentials for an access token.
func (in *instance) login() error {
	body, err := json.Marshal(map[string]string{"username": adminUser, "password": adminPassword})
	if err != nil {
		return fmt.Errorf("cannot encode the login request: %w", err)
	}
	resp, err := in.client.Post(in.baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("the login request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("cannot decode the login response: %w", err)
	}
	if envelope.Data.AccessToken == "" {
		return fmt.Errorf("login returned no access token (status %d)\n%s", resp.StatusCode, in.tail())
	}
	in.token = envelope.Data.AccessToken
	return nil
}

// stop kills the server and removes the instance directory. It is driven from
// TestMain rather than a test's cleanup so a panicking or timed-out test still
// leaves no process and no temp directory behind.
func (in *instance) stop() {
	if in.cmd != nil && in.cmd.Process != nil {
		_ = in.cmd.Process.Kill()
		_, _ = in.cmd.Process.Wait()
	}
	if in.dir != "" {
		_ = os.RemoveAll(in.dir)
	}
}

// tail returns the last of the server log, for failures that only the server
// can explain.
func (in *instance) tail() string {
	data, err := os.ReadFile(in.logPath)
	if err != nil {
		return "(no server log)"
	}
	const keep = 4000
	if len(data) > keep {
		data = data[len(data)-keep:]
	}
	return "--- server log tail ---\n" + string(data)
}

// do sends an authenticated JSON request and returns the status along with the
// raw body. Decoding is left to the caller so a test can assert on the
// envelope it actually expects.
func (in *instance) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err, "the request body for %s %s must encode", method, path)
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, in.baseURL+path, reader)
	require.NoError(t, err, "the request to %s %s must build", method, path)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+in.token)

	resp, err := in.client.Do(req)
	require.NoError(t, err, "%s %s must reach the server\n%s", method, path, in.tail())
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "the response body of %s %s must be readable", method, path)
	return resp.StatusCode, data
}

// data decodes the {"data": …} envelope of a successful request into out,
// failing the test with the body when the request did not succeed.
func (in *instance) data(t *testing.T, method, path string, body, out any) {
	t.Helper()
	status, raw := in.do(t, method, path, body)
	require.Lessf(t, status, 300, "%s %s answered %d: %s", method, path, status, raw)
	if out == nil {
		return
	}
	envelope := struct {
		Data json.RawMessage `json:"data"`
	}{}
	require.NoErrorf(t, json.Unmarshal(raw, &envelope), "%s %s returned an undecodable envelope: %s", method, path, raw)
	require.NoErrorf(t, json.Unmarshal(envelope.Data, out), "%s %s returned unexpected data: %s", method, path, envelope.Data)
}

// createFlow creates a flow with the given name and saves graph as its draft,
// returning the new flow's ID.
func (in *instance) createFlow(t *testing.T, name, graph string) string {
	t.Helper()

	var created struct {
		ID string `json:"id"`
	}
	in.data(t, http.MethodPost, "/api/flows", map[string]any{"name": name}, &created)
	require.NotEmptyf(t, created.ID, "creating the flow %q must return its id", name)

	var document any
	require.NoErrorf(t, json.Unmarshal([]byte(graph), &document), "the graph fixture for %q must be JSON", name)
	in.data(t, http.MethodPut, "/api/flows/"+created.ID+"/graph", map[string]any{"graph": document}, nil)
	return created.ID
}

// deploy freezes the flow's draft as a version.
func (in *instance) deploy(t *testing.T, flowID string) {
	t.Helper()
	in.data(t, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{"comment": "integration"}, nil)
}

// enable arms the deployed flow's triggers.
func (in *instance) enable(t *testing.T, flowID string) {
	t.Helper()
	in.data(t, http.MethodPost, "/api/flows/"+flowID+"/enable", map[string]any{}, nil)
}

// waitForRunStatus polls the flow's last run until it reaches want. Polling is
// the only way to observe a run: the engine is asynchronous and the test-run
// request returns as soon as the run is scheduled.
func (in *instance) waitForRunStatus(t *testing.T, flowID, want string) {
	t.Helper()

	status := ""
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// The run row is nested under the detail's events and node states, so
		// decode the field rather than scanning the body: every node event
		// carries a status of its own and a looser match reads one of those.
		var detail struct {
			Run struct {
				Status string `json:"status"`
			} `json:"run"`
		}
		code, raw := in.do(t, http.MethodGet, "/api/flows/"+flowID+"/run", nil)
		if code == http.StatusOK {
			envelope := struct {
				Data json.RawMessage `json:"data"`
			}{}
			if json.Unmarshal(raw, &envelope) == nil && json.Unmarshal(envelope.Data, &detail) == nil {
				status = detail.Run.Status
				if status == want {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equalf(t, want, status, "the run never reached %q\n%s", want, in.tail())
}
