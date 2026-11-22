package sandbox

import (
	"time"
)

// Options describes how to install the script runtimes at startup. It mirrors
// the script configuration section without importing it, so the sandbox
// package stays independent of configuration loading.
type Options struct {
	// Network names the outbound policy: "full", "bogons", or "none".
	Network string
	// TimeoutSeconds is the default per-execution budget.
	TimeoutSeconds int
	// MaxOutput caps captured console bytes per execution.
	MaxOutput int
	// MaxHTTPRequests caps outbound calls per execution.
	MaxHTTPRequests int
	// MaxHTTPResponse caps bytes read from one response body.
	MaxHTTPResponse int
	// MemoryLimitMB caps guest memory where the runtime can enforce it.
	MemoryLimitMB int

	// PythonEnabled controls whether the Python runtime is registered.
	PythonEnabled bool
	// PythonWasm is an explicit path to the CPython WebAssembly module.
	PythonWasm string
	// PythonSearchDirs are the directories searched when PythonWasm is empty.
	PythonSearchDirs []string
	// PythonCacheDir holds compiled-module caching across restarts.
	PythonCacheDir string
}

// Install registers the built-in runtimes and installs the process-wide
// policy. It returns the resolved policy, the Python module path (empty when
// Python is unavailable), and any warning worth logging. An unrecognized
// network policy is a warning rather than a failure: the server should still
// start, with the permissive default in effect and the problem visible.
func Install(opts Options) (NetworkPolicy, string, error) {
	policy, warn := ParseNetworkPolicy(opts.Network)

	SetDefaults(Limits{
		Timeout:         time.Duration(opts.TimeoutSeconds) * time.Second,
		MaxOutput:       opts.MaxOutput,
		MaxHTTPRequests: opts.MaxHTTPRequests,
		MaxHTTPResponse: opts.MaxHTTPResponse,
		MemoryLimitMB:   opts.MemoryLimitMB,
	}, policy)

	Register(JSRuntime{})
	Register(ShellRuntime{})

	var pythonPath string
	if opts.PythonEnabled {
		pythonPath = ResolvePythonWasm(opts.PythonWasm, opts.PythonSearchDirs)
		// The runtime registers even without a module so the node reports a
		// specific "not installed" error rather than "unknown language".
		Register(NewPythonRuntime(pythonPath, opts.PythonCacheDir, opts.MemoryLimitMB))
	}

	return policy, pythonPath, warn
}
