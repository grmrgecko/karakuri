package main

import (
	"path/filepath"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"go.uber.org/zap"
)

// initScriptSandbox installs the script runtimes and the network policy from
// configuration. Nothing here is fatal: a missing Python module or an
// unrecognized policy leaves the server running with the remaining languages,
// and the script node reports the specific reason when it is used.
func initScriptSandbox(log *zap.Logger) {
	cfg := config.C.Script

	policy, pythonPath, warn := sandbox.Install(sandbox.Options{
		Network:          cfg.Network,
		TimeoutSeconds:   cfg.Timeout,
		MaxOutput:        cfg.MaxOutput,
		MaxHTTPRequests:  cfg.MaxHTTPRequests,
		MaxHTTPResponse:  cfg.MaxHTTPResponse,
		MemoryLimitMB:    cfg.MemoryLimitMB,
		PythonEnabled:    cfg.PythonEnabled,
		PythonWasm:       cfg.PythonWasm,
		PythonSearchDirs: resources.GetResourcePaths(resources.Runtimes),
		PythonCacheDir:   filepath.Join(resources.GetDataPath(), "wasm-cache"),
	})
	if warn != nil {
		log.Warn("script network policy", zap.Error(warn))
	}

	log.Info("script sandbox installed",
		zap.String("network_policy", string(policy)),
		zap.Strings("languages", sandbox.Languages()))

	if cfg.PythonEnabled && pythonPath == "" {
		log.Warn("python script support is enabled but no python.wasm was found; " +
			"install it with `make runtimes` or set script.python_wasm")
	}

	// Compiling CPython takes seconds cold and well under a second from the
	// on-disk cache. Doing it in the background here means the first flow to
	// run a Python script does not pay for it, while startup stays fast.
	if pythonPath != "" {
		go warmPythonRuntime(log, pythonPath)
	}

	// The permissive default lets a script reach the LAN, the host's own
	// services, and cloud metadata endpoints. That is the documented default,
	// but it should be a deliberate choice rather than an unnoticed one.
	if policy == sandbox.NetworkFull {
		log.Info("scripts may reach any network destination; " +
			"set script.network to \"bogons\" to restrict them to public addresses")
	}
}

// warmPythonRuntime compiles the CPython module ahead of first use. A failure
// is logged and left alone: the script node reports the same error with more
// context when someone actually runs a Python script.
func warmPythonRuntime(log *zap.Logger, path string) {
	started := time.Now()
	rt, ok := sandbox.Get(sandbox.LangPython)
	if !ok {
		return
	}
	if err := rt.Available(); err != nil {
		log.Warn("python runtime failed to compile",
			zap.String("module", path), zap.Error(err))
		return
	}
	log.Info("python runtime ready",
		zap.String("module", path),
		zap.Duration("compile_time", time.Since(started).Round(time.Millisecond)))
}
