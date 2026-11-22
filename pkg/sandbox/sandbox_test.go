package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One CPython runtime is shared by every Python test in the binary. Compiling
// the 26 MB wasm module costs seconds — closer to half a minute under the race
// detector — and PythonRuntime is built to be reused: it compiles once per
// process and gives each Run its own scratch directory and module instance.
// Building one per test made every Python case pay for a fresh compile, which
// dominated the package's runtime and leaked into the wall-clock budget of the
// interruptibility test.
var (
	pythonOnce     sync.Once
	pythonRuntime  *PythonRuntime
	pythonCacheDir string
)

// TestMain owns the shared Python runtime's lifetime, since no single test can.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "karakuri-python-cache-")
	if err == nil {
		pythonCacheDir = dir
	}
	code := m.Run()
	if pythonRuntime != nil {
		_ = pythonRuntime.Close(context.Background())
	}
	if pythonCacheDir != "" {
		_ = os.RemoveAll(pythonCacheDir)
	}
	os.Exit(code)
}

// sharedPython returns the process-wide CPython runtime, skipping the test when
// the wasm module is not installed on this machine.
func sharedPython(t *testing.T) Runtime {
	t.Helper()
	pythonOnce.Do(func() {
		path := os.Getenv("KARAKURI_TEST_PYTHON_WASM")
		if path == "" {
			path = ResolvePythonWasm("", []string{"testdata", "../../build/runtimes"})
		}
		if path == "" {
			return
		}
		pythonRuntime = NewPythonRuntime(path, pythonCacheDir, 0)
	})
	if pythonRuntime == nil {
		t.Skip("python.wasm not installed; set KARAKURI_TEST_PYTHON_WASM or run make runtimes")
	}
	return pythonRuntime
}

// runtimeFor builds the runtime under test, skipping Python when the wasm
// module is not installed on the machine running the tests.
func runtimeFor(t *testing.T, language string) Runtime {
	t.Helper()
	switch language {
	case LangJavaScript:
		return JSRuntime{}
	case LangBash:
		return ShellRuntime{}
	case LangPython:
		return sharedPython(t)
	}
	require.FailNowf(t, "unknown language", "%q has no runtime", language)
	return nil
}

// warmRuntime pays any one-time compilation cost up front, so a test that
// measures elapsed time measures only the behavior it is checking.
func warmRuntime(t *testing.T, language string) {
	t.Helper()
	rt, ok := runtimeFor(t, language).(interface{ Available() error })
	if !ok {
		return
	}
	require.NoErrorf(t, rt.Available(), "the %s runtime must be usable before its behaviour is timed", language)
}

// run executes a script against a runtime with generous test limits.
func run(t *testing.T, language, source string, req Request) (*Result, error) {
	t.Helper()
	rt := runtimeFor(t, language)
	req.Language = language
	req.Source = source
	if req.Limits.Timeout == 0 {
		req.Limits.Timeout = 60 * time.Second
	}
	req.Limits = req.Limits.Normalize()
	if req.Network == "" {
		req.Network = NetworkFull
	}
	ctx, cancel := context.WithTimeout(context.Background(), req.Limits.Timeout)
	defer cancel()
	return rt.Run(ctx, req)
}

// allLanguages is the set every shared behavior test runs against.
var allLanguages = []string{LangJavaScript, LangPython, LangBash}

// scriptFor returns an equivalent script per language for a shared behavior.
type scriptFor map[string]string

// TestComputesResultFromPayload covers the sandbox's basic contract in all
// three languages at once: the message goes in, a value comes back. Each
// language expresses it differently — JavaScript returns, Python assigns to
// result, bash prints to stdout — and the three have to agree on what a script
// can read and what counts as its answer, or a flow author's mental model
// breaks the moment they switch language.
func TestComputesResultFromPayload(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `return { doubled: payload.value * 2, topic: topic };`,
		LangPython:     `result = {"doubled": payload["value"] * 2, "topic": topic}`,
		LangBash:       `v=$(jsonget value); echo "{\"doubled\": $((v*2)), \"topic\": \"$TOPIC\"}"`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{
				Payload: map[string]any{"value": 21},
				Topic:   "sensors/temp",
			})
			require.NoError(t, err, "the script must run")

			doc, ok := res.Value.(map[string]any)
			require.Truef(t, ok, "result is %T, want the object the script produced: %#v", res.Value, res.Value)
			assert.Equal(t, float64(42), doc["doubled"],
				"21 * 2 = 42: the payload must be readable and the computed value must come back")
			assert.Equal(t, "sensors/temp", doc["topic"],
				"a script sees the whole message, not only the payload")
		})
	}
}

// TestReadsInputsByPortName covers the named-socket channel. A script node may
// declare several inputs, and payload only ever carries the first — without
// this, every other wired socket would be unreachable and the declared ports
// would be decoration. All three languages must expose them under the same
// names the author typed.
func TestReadsInputsByPortName(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `return { c: input.celsius, where: input.label };`,
		LangPython:     `result = {"c": input["celsius"], "where": input["label"]}`,
		LangBash: `c=$(jsonget celsius "$INPUT"); w=$(jsonget label "$INPUT");` +
			` echo "{\"c\": $c, \"where\": \"$w\"}"`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{
				Payload: 21.5,
				Inputs:  map[string]any{"celsius": 21.5, "label": "attic"},
			})
			require.NoError(t, err, "the script must run")

			doc, ok := res.Value.(map[string]any)
			require.Truef(t, ok, "result is %T, want an object: %#v", res.Value, res.Value)
			assert.Equal(t, 21.5, doc["c"], "each socket must be readable under the name its author declared")
			assert.Equal(t, "attic", doc["where"], "a second socket must be reachable too, not only the one behind payload")
		})
	}
}

// TestCapturesConsoleOutput covers the only way a script author can see inside
// their own code. The sandbox has no terminal, so printing has to be captured
// and handed back or debugging is guesswork — and all three languages have to
// do it, since bash writes to a real stdout and the other two do not.
func TestCapturesConsoleOutput(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `console.log("hello", 1); return 1;`,
		LangPython:     "print('hello', 1)\nresult = 1",
		LangBash:       `log hello 1; echo 1`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{})
			require.NoError(t, err, "printing must not fail the script")
			assert.Contains(t, strings.Join(res.Output, "\n"), "hello 1",
				"what the script printed must come back, or the debug panel shows nothing")
		})
	}
}

// TestScriptErrorsSurface covers a script that fails. Each language signals it
// differently — an exception in two of them, a non-zero exit status in bash —
// and all three have to become a Go error carrying enough to identify the
// failure, or the node succeeds with no result and the flow carries on.
func TestScriptErrorsSurface(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `throw new Error("boom");`,
		LangPython:     `raise ValueError("boom")`,
		LangBash:       `echo boom >&2; exit 3`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			_, err := run(t, lang, scripts[lang], Request{})
			require.Error(t, err, "a failing script must fail the node, not succeed with no result")
			assert.Truef(t,
				strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "status 3"),
				"the error must identify the failure, or the author has nothing to debug from: %v", err)
		})
	}
}

// TestNoFilesystemAccess is the isolation guarantee the whole sandbox exists
// for. Script nodes are edited by anyone who can open a flow, so a script that
// could read the host filesystem would hand every such person the server's
// config, its database, and its keys. /etc/passwd is used because it is
// certain to exist and its absence means the check would prove nothing.
//
// Each language is checked the way it would actually escape: JavaScript by
// looking for any host API at all, Python by trying open(), and bash by
// invoking a command that reads a file.
func TestNoFilesystemAccess(t *testing.T) {
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Skip("/etc/passwd not present")
	}
	scripts := scriptFor{
		LangJavaScript: `
			// A bare VM has no file API at all; prove none was installed.
			return { require: typeof require, process: typeof process,
			         open: typeof open, readFile: typeof readFile };`,
		LangPython: `
try:
    result = {"leaked": open("/etc/passwd").read()[:20]}
except Exception as e:
    result = {"blocked": type(e).__name__}`,
		LangBash: `cat /etc/passwd`,
	}

	t.Run(LangJavaScript, func(t *testing.T) {
		res, err := run(t, LangJavaScript, scripts[LangJavaScript], Request{})
		require.NoError(t, err, "the probe script must run")

		doc, ok := res.Value.(map[string]any)
		require.True(t, ok, "the probe returns an object of type names")
		for name, kind := range doc {
			assert.Equalf(t, "undefined", kind,
				"%s exists in the VM: the sandbox must install no host API a script could reach a file through", name)
		}
	})

	t.Run(LangPython, func(t *testing.T) {
		res, err := run(t, LangPython, scripts[LangPython], Request{})
		require.NoError(t, err, "the probe script must run")

		doc, ok := res.Value.(map[string]any)
		require.True(t, ok, "the probe returns an object")
		assert.NotContains(t, doc, "leaked", "the script read the host filesystem: %v", doc["leaked"])
		assert.NotNil(t, doc["blocked"], "open() must raise rather than succeed: %#v", doc)
	})

	t.Run(LangBash, func(t *testing.T) {
		_, err := run(t, LangBash, scripts[LangBash], Request{})
		require.Error(t, err, "reading a host file must be denied")
		assert.Contains(t, err.Error(), "not available in the sandbox",
			"the denial must be explicit, so an author knows it was refused rather than that the file was missing: %v", err)
	})
}

// TestBashCannotProbeHostFilesystem covers the shell's ways of seeing the host
// without opening a file. The interpreter expands globs, answers file tests,
// and reports a working directory through handlers of its own, separate from
// the open handler, and each defaulted to the real filesystem: a script could
// list any directory with a glob and test any path for existence, which names
// the server's files to anyone who can edit a flow.
func TestBashCannotProbeHostFilesystem(t *testing.T) {
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Skip("/etc/passwd not present")
	}
	hostDir, err := os.Getwd()
	require.NoError(t, err, "the test needs the host working directory to compare against")

	cases := []struct {
		name   string
		source string
		want   string
		why    string
	}{
		{
			name:   "glob",
			source: `printf '%s\n' /et*`,
			want:   "/et*",
			why:    "a glob must match nothing and stay literal rather than list a host directory",
		},
		{
			name:   "file test",
			source: `if [ -e /etc/passwd ]; then echo found; else echo hidden; fi`,
			want:   "hidden",
			why:    "a file test must not report whether a host path exists",
		},
		{
			name:   "conditional expression",
			source: `if [[ -d /etc ]]; then echo found; else echo hidden; fi`,
			want:   "hidden",
			why:    "a conditional expression must not report whether a host directory exists",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := run(t, LangBash, c.source, Request{})
			require.NoError(t, err, "the probe script must run")
			assert.Equal(t, c.want, res.Value, c.why)
		})
	}

	t.Run("working directory", func(t *testing.T) {
		res, err := run(t, LangBash, `echo "$PWD"; pwd`, Request{})
		require.NoError(t, err, "the probe script must run")
		assert.NotContains(t, res.Value, hostDir,
			"the script must not learn the directory the server runs in")
	})
}

// TestPythonCannotEscapeScratchDirectory covers the one host directory a
// Python script can reach. Its scratch directory is mounted read-write, and a
// mount that resolves symbolic links on the host lets a script plant a link to
// any path and read through it, which is the same leak open("/etc/passwd")
// would be with one more step.
func TestPythonCannotEscapeScratchDirectory(t *testing.T) {
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Skip("/etc/passwd not present")
	}
	// An absolute target is refused by path resolution on its own; a relative
	// one climbing out of the mount is the form that used to resolve.
	res, err := run(t, LangPython, `
import os
result = {}
for name, target in [("relative", "../" * 12 + "etc/passwd"), ("absolute", "/etc/passwd")]:
    try:
        os.symlink(target, "/karakuri/" + name)
        result[name] = "leaked " + open("/karakuri/" + name).read()[:20]
    except Exception as e:
        result[name] = "blocked " + type(e).__name__`, Request{})
	require.NoError(t, err, "the probe script must run")

	doc, ok := res.Value.(map[string]any)
	require.True(t, ok, "the probe returns an object")
	for _, name := range []string{"relative", "absolute"} {
		assert.Containsf(t, doc[name], "blocked",
			"a %s link out of the scratch directory must not be readable: %v", name, doc[name])
	}
}

// TestPythonCannotOpenSockets closes the other half of the isolation. Outbound
// HTTP is the sandbox's one host capability and it goes through a bridge that
// applies script.network; a raw socket would go around that policy entirely,
// reaching the local network and any cloud metadata endpoint with it. Python is
// the case that matters because its standard library ships socket support.
func TestPythonCannotOpenSockets(t *testing.T) {
	res, err := run(t, LangPython, `
import socket
try:
    s = socket.socket()
    s.connect(("1.1.1.1", 80))
    result = "REACHED"
except Exception as e:
    result = "blocked: " + type(e).__name__
`, Request{})
	require.NoError(t, err, "the probe script must run")
	assert.NotEqual(t, "REACHED", res.Value,
		"a raw socket bypasses the network policy entirely, reaching the local network and any metadata endpoint")
}

// TestTimeoutStopsRunawayScript covers the hardest thing to get right about
// running someone else's code: stopping it. An infinite loop yields nothing, so
// a runtime that cannot be interrupted from outside will run until the process
// dies — this is the check that each of the three really can be.
//
// The wall-clock bound is generous because it is a liveness check rather than a
// performance one: what fails here is a runtime that never returns at all.
func TestTimeoutStopsRunawayScript(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `while (true) {}`,
		LangPython:     `while True: pass`,
		LangBash:       `while true; do :; done`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			// Compile before timing: this test is about whether a running script
			// can be stopped, not about how long the runtime takes to build.
			warmRuntime(t, lang)

			start := time.Now()
			_, err := run(t, lang, scripts[lang], Request{
				Limits: Limits{Timeout: 2 * time.Second},
			})
			require.Error(t, err, "a script that never finishes must be stopped and reported")
			assert.Lessf(t, time.Since(start), 30*time.Second,
				"the %s runtime did not yield to the timeout, so a runaway script would run until the process died", lang)
		})
	}
}

// ---- Network policy ----

// TestHTTPBridgeReachesAllowedHost covers the sandbox's one host capability
// working at all, against a real HTTP server. It is the positive case the
// denial tests below are measured against: without it, a policy that blocked
// everything would look identical to a bridge that was simply broken.
func TestHTTPBridgeReachesAllowedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"method":"` + r.Method + `"}`))
	}))
	defer server.Close()

	scripts := scriptFor{
		LangJavaScript: `var r = http("` + server.URL + `"); return r.json();`,
		LangPython:     `result = get("` + server.URL + `").json()` + "\nfrom karakuri import get",
		LangBash:       `http GET "` + server.URL + `"`,
	}
	// Python needs the import before use; rewrite in the right order.
	scripts[LangPython] = "import karakuri\nresult = karakuri.get(\"" + server.URL + "\").json()"

	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			// The test server listens on loopback, which the bogons policy
			// blocks, so this case runs under the permissive policy.
			res, err := run(t, lang, scripts[lang], Request{Network: NetworkFull})
			require.NoError(t, err, "an allowed request must succeed")

			doc, ok := res.Value.(map[string]any)
			require.Truef(t, ok, "result is %T, want the decoded response: %#v", res.Value, res.Value)
			assert.Equal(t, true, doc["ok"],
				"the server's own document must reach the script, or the bridge is not really carrying the response")
		})
	}
}

// TestNetworkNoneDeniesEverything covers the strictest policy, which is what an
// operator sets when scripts have no business reaching anything. Against the
// same live server the allowed case above reaches, so the denial is the policy
// and not an unreachable host. Each language reports it in its own idiom — an
// error field, an exception, a non-zero exit — and all three must actually
// fail rather than quietly return nothing.
func TestNetworkNoneDeniesEverything(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"reached":true}`))
	}))
	defer server.Close()

	scripts := scriptFor{
		LangJavaScript: `var r = http("` + server.URL + `"); return { err: r.error };`,
		LangPython: `
import karakuri
try:
    karakuri.get("` + server.URL + `")
    result = {"err": ""}
except karakuri.HTTPError as e:
    result = {"err": str(e)}`,
		LangBash: `http GET "` + server.URL + `" || echo '{"err":"denied"}'`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{Network: NetworkNone})
			require.NoError(t, err, "the script itself runs; only its request is refused")

			doc, ok := res.Value.(map[string]any)
			require.Truef(t, ok, "result is %T: %#v", res.Value, res.Value)
			assert.NotEmpty(t, doc["err"],
				"the request reached a live server, so an empty error means the policy did not apply: %#v", doc)
		})
	}
}

// TestNetworkBogonsBlocksLoopback covers the policy an operator actually wants:
// scripts may call the internet but not the machine they run on or the network
// around it. Loopback is the sharpest case — the karakuri API itself is there,
// as is anything else the host runs — and the test server is on loopback, so a
// policy that only filtered by name rather than by resolved address would let
// it straight through.
func TestNetworkBogonsBlocksLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"reached":true}`))
	}))
	defer server.Close()

	res, err := run(t, LangJavaScript,
		`var r = http("`+server.URL+`"); return { err: r.error, status: r.status };`,
		Request{Network: NetworkBogons})
	require.NoError(t, err, "the script itself runs; only its request is refused")

	doc, ok := res.Value.(map[string]any)
	require.Truef(t, ok, "result is %T: %#v", res.Value, res.Value)
	assert.NotEmpty(t, doc["err"],
		"loopback was reachable, which is where the karakuri API itself listens: %#v", doc)
}

// TestIsBogon pins the address ranges the bogons policy refuses, which are
// facts about IP addressing rather than about this code. The rows that earn
// their place are the ones a hand-written check forgets: 169.254.169.254 is the
// cloud metadata endpoint that hands out credentials, 100.64/10 is
// carrier-grade NAT, and ::ffff:127.0.0.1 is loopback wearing an IPv6 shape —
// each is a way to reach the host or its network past a naive private-range
// test.
func TestIsBogon(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.169.254", true}, // Cloud metadata endpoint.
		{"100.64.0.1", true},      // Carrier-grade NAT.
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::1", true},
		{"fd00::1", true},
		{"fe80::1", true},
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback.
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range cases {
		addr, err := netip.ParseAddr(tc.addr)
		require.NoErrorf(t, err, "the fixture address %s must parse", tc.addr)
		assert.Equalf(t, tc.want, IsBogon(addr),
			"IsBogon(%s) decides whether a script can reach it", tc.addr)
	}
}

// TestParseNetworkPolicy covers reading script.network out of the config file.
// An unset value means full, which is what keeps an existing install working
// after the setting was added; a name nobody recognises is refused rather than
// falling back, since defaulting a security setting to permissive on a typo is
// exactly the wrong direction to fail in.
func TestParseNetworkPolicy(t *testing.T) {
	cases := map[string]NetworkPolicy{
		"":       NetworkFull,
		"full":   NetworkFull,
		"none":   NetworkNone,
		"bogons": NetworkBogons,
		"BOGONS": NetworkBogons,
	}
	for in, want := range cases {
		got, err := ParseNetworkPolicy(in)
		if assert.NoErrorf(t, err, "%q is a value an operator may write and must parse", in) {
			assert.Equalf(t, want, got, "ParseNetworkPolicy(%q)", in)
		}
	}

	_, err := ParseNetworkPolicy("wide-open")
	assert.Error(t, err,
		"an unrecognised policy must be refused: defaulting a security setting to permissive on a typo fails the wrong way")
}

// TestOutputTruncation covers the cap on captured output. Console output is
// stored in run history and rendered in the debug panel, so a script printing
// in a loop would otherwise write a hundred thousand lines into the database
// and into the browser. The marker matters as much as the cap: output that was
// silently cut short would have an author looking for a bug in a script that
// ran perfectly.
func TestOutputTruncation(t *testing.T) {
	res, err := run(t, LangJavaScript,
		`for (var i = 0; i < 100000; i++) { console.log("padding line " + i); } return 1;`,
		Request{Limits: Limits{MaxOutput: 1000, Timeout: 30 * time.Second}})
	require.NoError(t, err, "printing a great deal must not fail the script")

	joined := strings.Join(res.Output, "\n")
	// The cap is 1000 bytes; the slack allows for the marker and a partial line.
	assert.Lessf(t, len(joined), 2000,
		"captured %d bytes: unbounded output would be written to the database and rendered in the browser", len(joined))
	assert.Contains(t, joined, "truncated",
		"the cut must be marked, or an author hunts for a bug in a script that ran perfectly")
}

// TestReadsAndWritesRunVars covers the variable channel in both directions.
// Writes come back in the result rather than being applied by the sandbox,
// which is what lets the script test button run a script without changing what
// a live flow reads — and it means each language has to report its writes
// rather than mutating anything.
func TestReadsAndWritesRunVars(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `vars.greeting = "hi " + vars.name; vars.count = vars.count + 1; return vars.name;`,
		LangPython: `
vars["greeting"] = "hi " + vars["name"]
vars["count"] = vars["count"] + 1
result = vars["name"]
`,
		LangBash: `
name=$(jsonget name "$VARS")
count=$(jsonget count "$VARS")
setvar -s run greeting "hi $name"
setvar -s run count $((count+1))
echo -n "$name"
`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{
				Vars: map[string]any{"name": "office", "count": float64(1)},
			})
			require.NoError(t, err, "the script must run")

			assert.Equal(t, "office", res.Value, "the variables the run already holds must be readable")
			assert.Equal(t, "hi office", res.Vars["greeting"],
				"a new variable must come back in the result for the engine to apply, not be written by the sandbox")
			assert.Equal(t, float64(2), res.Vars["count"], "1 + 1 = 2: a read-modify-write must round trip")
		})
	}
}

// TestRunVarsAbsentAreEmpty covers the first script in a run, before anything
// has set a variable. The vars object has to exist and be writable rather than
// be undefined, or every script would need a guard before its first write and
// the commonest use — a script that seeds a value for later nodes — would fail
// on the one run that matters.
func TestRunVarsAbsentAreEmpty(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `vars.set_here = true; return Object.keys(vars).length;`,
		LangPython:     "vars[\"set_here\"] = True\nresult = len(vars)",
		LangBash:       `setvar -s run set_here true; echo 1`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{})
			require.NoError(t, err, "writing to an empty variable scope must not fail")
			assert.Equal(t, float64(1), res.Value,
				"the scope must exist and be empty, so the one write leaves exactly one variable in it")
			assert.Equal(t, true, res.Vars["set_here"], "the write must still be reported back: %#v", res.Vars)
		})
	}
}

// TestScopedGetSetDefaultsToMessage covers the default scope of the get/set
// pair. Omitting the scope reads and writes the message, which is what a script
// most often wants; a different default in one language would make the same
// script mean different things depending on which one it was written in.
func TestScopedGetSetDefaultsToMessage(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `
const name = get("name");
set("greeting", "hi " + name);
return get("payload").name;
`,
		LangPython: `
name = get("name")
set("greeting", "hi " + name)
result = get("payload")["name"]
`,
		LangBash: `
name=$(getvar name)
setvar greeting "hi $name"
getvar name
`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{
				Payload: map[string]any{"name": "office"},
			})
			require.NoError(t, err, "the script must run")
			assert.Equal(t, "office", res.Value, "a bare name reads out of the payload, without naming a scope")

			require.Lenf(t, res.MsgWrites, 1, "the write must be reported for the node to apply: %#v", res.MsgWrites)
			assert.Equal(t, "greeting", res.MsgWrites[0].Path, "the write must name where it goes")
			assert.Equal(t, "hi office", res.MsgWrites[0].Value, "the write must carry what it read and computed")
		})
	}
}

// TestScopedGetSetFlowAndGlobal covers the two scopes that outlive a run. A
// script reads what is already stored and its writes come back separated by
// scope, so the engine persists each in the right place — a flow variable
// written into the global store would be visible to every other flow on the
// instance.
func TestScopedGetSetFlowAndGlobal(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `
const seen = get("count", "flow");
set("count", seen + 1, "flow");
set("site", get("site", "global") + "!", "global");
return seen;
`,
		LangPython: `
seen = get("count", "flow")
set("count", seen + 1, "flow")
set("site", get("site", "global") + "!", "global")
result = seen
`,
		LangBash: `
seen=$(getvar -s flow count)
setvar -s flow count $((seen+1))
setvar -s global site "$(getvar -s global site)!"
echo -n "$seen"
`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{
				Flow:   map[string]any{"count": float64(4)},
				Global: map[string]any{"site": "hq"},
			})
			require.NoError(t, err, "the script must run")

			assert.Equal(t, float64(4), res.Value, "the persisted flow variable must be readable as it stands")
			assert.Equal(t, float64(5), res.Flow["count"], "4 + 1 = 5: the write must come back under the flow scope")
			assert.Equal(t, "hq!", res.Global["site"],
				"a global write must come back under the global scope: crossing them would leak state between flows")
		})
	}
}

// TestBashConcurrentCommandsShareState covers the goroutines the shell
// interpreter starts on its own: the left side of a pipeline and every `&`
// statement run concurrently with the rest of the script. Unsynchronized
// setvar writes there raced on the scope map, and Go aborts the whole process
// on a concurrent map write, so one script could take the server down. The
// same goroutines write the script's stdout.
func TestBashConcurrentCommandsShareState(t *testing.T) {
	t.Run("pipelined setvar writes all land", func(t *testing.T) {
		res, err := run(t, LangBash,
			`for ((i = 0; i < 200; i++)); do
  setvar -s flow a$i 1 | setvar -s flow b$i 2
done`, Request{})
		require.NoError(t, err, "the script must run")
		for i := 0; i < 200; i++ {
			assert.Containsf(t, res.Flow, fmt.Sprintf("a%d", i), "the pipeline's left side must write a%d", i)
			assert.Containsf(t, res.Flow, fmt.Sprintf("b%d", i), "the pipeline's right side must write b%d", i)
		}
	})
	t.Run("background echoes reach stdout", func(t *testing.T) {
		res, err := run(t, LangBash, `echo a & echo b & wait`, Request{})
		require.NoError(t, err, "the script must run")
		out, ok := res.Value.(string)
		require.Truef(t, ok, "two lines of text must come back as a string, got %T", res.Value)
		assert.ElementsMatch(t, []string{"a", "b"}, strings.Fields(out),
			"both background commands must write their line to the result")
	})
}

// TestScopedSetRejectsUnknownScope covers a misspelled scope name, beside the
// three working scopes above. It fails the script rather than writing
// somewhere nothing reads, and names the bad scope — with four valid spellings
// and no compiler to catch a typo, the message is the only thing that points at
// which one was wrong.
func TestScopedSetRejectsUnknownScope(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `set("x", 1, "flwo"); return 1;`,
		LangPython:     `set("x", 1, "flwo")`,
		LangBash:       `setvar -s flwo x 1`,
	}
	for _, lang := range allLanguages {
		t.Run(lang, func(t *testing.T) {
			_, err := run(t, lang, scripts[lang], Request{})
			require.Error(t, err, "a misspelled scope must fail rather than write where nothing reads")
			assert.Containsf(t, err.Error(), "flwo",
				"the error must name the bad scope: there is no compiler to catch this typo: %v", err)
		})
	}
}

// TestRunDoesNotAliasScopeValues covers what a script may do to the variable
// scopes it is handed. The scopes arrive as snapshots of stores every run
// shares, and a snapshot copies only the top level, so an object or list held
// in a variable is the stored one. JavaScript reads Go maps as live objects:
// writing inside a nested variable changed the store itself while other runs
// read it, and did so even when the script then failed and committed nothing.
func TestRunDoesNotAliasScopeValues(t *testing.T) {
	Register(JSRuntime{})
	scopes := map[string]string{"vars": "run", "flow": "flow", "global": "global"}
	for object, name := range scopes {
		t.Run(name, func(t *testing.T) {
			nested := map[string]any{"count": 1.0}
			list := []any{"a"}
			stored := map[string]any{"state": nested, "list": list}
			req := Request{
				Language: LangJavaScript,
				Source:   object + `.state.count = 2; ` + object + `.list[0] = "b"; throw new Error("stop");`,
			}
			switch object {
			case "vars":
				req.Vars = stored
			case "flow":
				req.Flow = stored
			default:
				req.Global = stored
			}

			_, err := Run(context.Background(), req)
			require.Error(t, err, "the script throws after writing, so nothing it did may be committed")
			assert.Equal(t, 1.0, nested["count"],
				"a write inside a nested variable must stay in the script's copy, not reach the shared store")
			assert.Equal(t, "a", list[0],
				"a write into a list variable must stay in the script's copy, not reach the shared store")
		})
	}
}

// TestRunDoesNotAliasTypedCollections locks the copy rule for values a node
// built with a concrete Go type. A database row list arrives as a slice of
// maps and a header set as a map of string lists, and JavaScript reads both as
// live objects, so a write inside one changed the message the upstream node
// had already emitted to its other branches and to run history.
func TestRunDoesNotAliasTypedCollections(t *testing.T) {
	Register(JSRuntime{})
	row := map[string]any{"n": 1.0}
	tags := []string{"a"}
	headers := map[string][]string{"accept": {"text/plain"}}
	payload := map[string]any{"rows": []map[string]any{row}, "tags": tags, "headers": headers}

	res, err := Run(context.Background(), Request{
		Language: LangJavaScript,
		Source: `payload.rows[0].n = 2; payload.tags[0] = "b"; payload.headers.accept[0] = "x";
return [payload.rows[0].n, payload.tags[0], payload.headers.accept[0]];`,
		Payload: payload,
	})
	require.NoError(t, err, "a script must be able to write inside its own copy of a typed collection")
	assert.Equal(t, []any{2.0, "b", "x"}, res.Value,
		"the script must still read and write the collection as an ordinary object")
	assert.Equal(t, 1.0, row["n"],
		"a write inside a typed list of objects must stay in the script's copy, not reach the upstream message")
	assert.Equal(t, "a", tags[0],
		"a write into a typed list must stay in the script's copy, not reach the upstream message")
	assert.Equal(t, "text/plain", headers["accept"][0],
		"a write inside a typed map must stay in the script's copy, not reach the upstream message")
}

// TestJavaScriptErrorsNameTheAuthorsLine locks that a JavaScript failure
// points at the line the author wrote. The source runs inside a one-line
// wrapper, and both the syntax error and the stack of a thrown error counted
// the wrapper's line, so every report was one line below the fault.
func TestJavaScriptErrorsNameTheAuthorsLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
		not    string
	}{
		{"a syntax error on the first line", "return 1 +* 2;", "Line 1:11", "Line 2"},
		{"a syntax error on a later line", "var a = 1;\nvar b = ;\nreturn a;", "Line 2:9", "Line 3"},
		{"a thrown error", "var a = 1;\nthrow new Error('boom');", "script.js:2:7", "script.js:3"},
		{"an error thrown inside a function", "function f() { null.x }\n\nf();", "script.js:1:21", "script.js:4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, LangJavaScript, tc.source, Request{})
			require.Error(t, err, "the script is broken and must fail")
			assert.Containsf(t, err.Error(), tc.want, "the report must name the author's own line and column: %v", err)
			assert.NotContainsf(t, err.Error(), tc.not, "the wrapper's line must not appear in the report: %v", err)
		})
	}

	out, err := run(t, LangJavaScript, "return 'script.js:9:9 is only text';", Request{})
	require.NoError(t, err, "a script that mentions a position in its own data is not an error")
	assert.Equal(t, "script.js:9:9 is only text", out.Value, "a value the script returns is never rewritten")
}

// TestAbsentMetaIsAnEmptyObject locks that a script can read meta on a message
// that carries no metadata. Many nodes emit messages without any, and the
// nil map reached JavaScript as undefined and Python as None, so a script
// reading meta.source failed on those messages only. Expressions and every
// other collection a script receives already see an empty object.
func TestAbsentMetaIsAnEmptyObject(t *testing.T) {
	scripts := scriptFor{
		LangJavaScript: `return [meta.source === undefined, msg.meta.source === undefined];`,
		LangPython:     `result = [meta.get("source") is None, msg["meta"].get("source") is None]`,
	}
	for _, lang := range []string{LangJavaScript, LangPython} {
		t.Run(lang, func(t *testing.T) {
			res, err := run(t, lang, scripts[lang], Request{Payload: 1})
			require.NoError(t, err, "reading a field of absent metadata must not fail the script")
			assert.Equal(t, []any{true, true}, res.Value,
				"meta and msg.meta are both an empty object when the message has no metadata")
		})
	}
}

// TestBashJSONGetPrintsNumbersInPlainDigits locks that jsonget prints a number
// the way shell arithmetic reads one. Numbers were printed with Go's default
// float form, so a Unix timestamp of 1764547200 came back as 1.7645472e+09
// and $(( ... )) over it failed, while getvar printed the same value in full.
func TestBashJSONGetPrintsNumbersInPlainDigits(t *testing.T) {
	res, err := run(t, LangBash, `ts=$(jsonget ts); echo "\"$ts\" $(( ts + 1 ))"`,
		Request{Payload: map[string]any{"ts": float64(1764547200)}})
	require.NoError(t, err, "arithmetic over a number read with jsonget must work")
	assert.Equal(t, `"1764547200" 1764547201`, res.Value,
		"a whole number prints as plain digits, not in exponent form")
}

// TestBashJSONGetKeepsLargeIntegers locks that jsonget prints an integer it
// reads from JSON text with the digits the document wrote. The document was
// decoded into float64, which rounds past 2^53, so a nanosecond timestamp or a
// 64-bit ID from an HTTP response body came back changed. Numbers written
// with a fraction or an exponent still print as their plain value.
func TestBashJSONGetKeepsLargeIntegers(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{name: "a nanosecond timestamp past 2^53", doc: `{"n":1764547200123456789}`, want: "1764547200123456789"},
		{name: "a whole number of 22 digits", doc: `{"n":1000000000000000000000}`, want: "1000000000000000000000"},
		{name: "a negative 64-bit ID", doc: `{"n":-9007199254740993}`, want: "-9007199254740993"},
		{name: "an exponent form", doc: `{"n":1e3}`, want: "1000"},
		{name: "a whole number written with a fraction", doc: `{"n":1.0}`, want: "1"},
		{name: "a fraction", doc: `{"n":21.5}`, want: "21.5"},
		{name: "a large integer inside a container", doc: `{"n":{"id":1764547200123456789}}`, want: `{"id":1764547200123456789}`},
	}
	for _, tc := range cases {
		// The prefix keeps the output text, so the result is not parsed back
		// into a number on the way out.
		res, err := run(t, LangBash, `n=$(jsonget n '`+tc.doc+`'); echo "got $n"`, Request{})
		require.NoError(t, err, "%s: jsonget must read the document", tc.name)
		assert.Equal(t, "got "+tc.want, res.Value, "%s", tc.name)
	}
}

// TestBashJSONGetArrayIndex locks that a path segment indexes an array only
// when the whole segment is a number. The index was scanned with %d, which
// stops at the first non-digit, so items.1abc read items[1] instead of
// reporting a missing path with exit status 1.
func TestBashJSONGetArrayIndex(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "items.1", want: "got 20"},
		{path: "items.1abc", want: "missing"},
	}
	for _, tc := range cases {
		res, err := run(t, LangBash,
			`n=$(jsonget `+tc.path+` '{"items":[10,20]}') && echo "got $n" || echo missing`, Request{})
		require.NoErrorf(t, err, "%s: the script must run", tc.path)
		assert.Equalf(t, tc.want, res.Value, "jsonget %s must index only with a whole-number segment", tc.path)
	}
}
