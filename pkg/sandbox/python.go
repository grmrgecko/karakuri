package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/tetratelabs/wazero"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// PythonRuntime executes Python by running a CPython build compiled to
// WebAssembly under wazero. WASI gives the guest no sockets and no directory
// it was not explicitly granted, so a script sees an empty world plus the two
// mounts this runtime provides: a per-run scratch directory and RPC files that
// perform network and eligible node calls under their respective policies.
//
// The wasm module is an external asset rather than an embedded one because it
// is 26 MB; when it is absent the runtime reports itself unavailable and the
// script node fails with an actionable message instead of misbehaving.
type PythonRuntime struct {
	// wasmPath is the CPython wasm module. Empty means "resolve from the
	// standard resource locations".
	wasmPath string
	// cacheDir holds wazero's on-disk compilation cache. Compiling CPython
	// takes several seconds cold and well under a second from cache.
	cacheDir string
	// memoryLimitMB caps guest memory per script. Zero means the default.
	memoryLimitMB int

	once     sync.Once
	initErr  error
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

// NewPythonRuntime builds a Python runtime. Both paths may be empty, in which
// case the module is resolved from the standard locations and compilation
// results are not cached across restarts.
func NewPythonRuntime(wasmPath, cacheDir string, memoryLimitMB int) *PythonRuntime {
	return &PythonRuntime{wasmPath: wasmPath, cacheDir: cacheDir, memoryLimitMB: memoryLimitMB}
}

func (*PythonRuntime) Language() string { return LangPython }

// pythonWasmNames are the module filenames looked for in the resource paths.
var pythonWasmNames = []string{"python.wasm", "python-3.12.0.wasm", "python-3.11.4.wasm"}

// ResolvePythonWasm finds the CPython wasm module in the given search
// directories, returning the first match.
func ResolvePythonWasm(explicit string, searchDirs []string) string {
	if explicit != "" {
		return explicit
	}
	for _, dir := range searchDirs {
		for _, name := range pythonWasmNames {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

// Available reports whether the wasm module is present and compiled. The
// first call pays the compilation cost; later calls are cheap.
func (p *PythonRuntime) Available() error {
	p.once.Do(p.init)
	return p.initErr
}

// init loads and compiles the CPython module once for the process lifetime.
func (p *PythonRuntime) init() {
	if p.wasmPath == "" {
		p.initErr = errors.New("no python.wasm module configured or found; " +
			"install it with `make runtimes` or set script.python_wasm")
		return
	}
	binary, err := os.ReadFile(p.wasmPath)
	if err != nil {
		p.initErr = fmt.Errorf("reading %s: %w", p.wasmPath, err)
		return
	}

	ctx := context.Background()
	// Without a page limit wazero lets a guest grow to the 4 GB a 32-bit
	// module can address, and script.memory_limit_mb meant nothing. A page is
	// 64 KiB, so 16 pages make a megabyte; 65536 pages is the ceiling.
	limitMB := p.memoryLimitMB
	if limitMB <= 0 {
		limitMB = defaultMemoryLimitMB
	}
	if limitMB > 4096 {
		limitMB = 4096
	}
	config := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(uint32(limitMB * 16))
	if p.cacheDir != "" {
		if err := os.MkdirAll(p.cacheDir, 0o755); err == nil {
			if cache, err := wazero.NewCompilationCacheWithDir(p.cacheDir); err == nil {
				config = config.WithCompilationCache(cache)
			}
		}
	}

	p.runtime = wazero.NewRuntimeWithConfig(ctx, config)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, p.runtime); err != nil {
		p.initErr = fmt.Errorf("instantiating WASI: %w", err)
		return
	}
	compiled, err := p.runtime.CompileModule(ctx, binary)
	if err != nil {
		p.initErr = fmt.Errorf("compiling %s: %w", p.wasmPath, err)
		return
	}
	p.compiled = compiled
}

// Close releases the compiled module and runtime.
func (p *PythonRuntime) Close(ctx context.Context) error {
	if p.runtime == nil {
		return nil
	}
	return p.runtime.Close(ctx)
}

// pythonOutput is the document the wrapper writes describing the outcome.
type pythonOutput struct {
	OK        bool           `json:"ok"`
	Value     any            `json:"value"`
	Vars      map[string]any `json:"vars"`
	Flow      map[string]any `json:"flow"`
	Global    map[string]any `json:"global"`
	MsgWrites []MsgWrite     `json:"msg_writes"`
	Error     string         `json:"error"`
}

// Run executes a script in a fresh guest instance. Each run gets its own
// scratch directory and its own module instance, so nothing carries over
// between executions.
func (p *PythonRuntime) Run(ctx context.Context, req Request) (*Result, error) {
	if err := p.Available(); err != nil {
		return nil, err
	}

	// Stage the scratch directory the guest will see as /karakuri.
	scratch, err := os.MkdirTemp("", "karakuri-py-")
	if err != nil {
		return nil, fmt.Errorf("creating scratch directory: %w", err)
	}
	defer os.RemoveAll(scratch)

	input, err := json.Marshal(map[string]any{
		"payload": req.Payload,
		"topic":   req.Topic,
		"meta":    orEmptyMap(req.Meta),
		"input":   orEmptyMap(req.Inputs),
		"vars":    orEmptyMap(req.Vars),
		"flow":    orEmptyMap(req.Flow),
		"global":  orEmptyMap(req.Global),
	})
	if err != nil {
		return nil, fmt.Errorf("encoding script input: %w", err)
	}
	files := map[string]string{
		"input.json":  string(input),
		"karakuri.py": pythonHelperModule,
		"main.py":     pythonMainModule,
		"script.py":   req.Source,
	}
	if req.NodeInvoker != nil {
		definitions, err := pythonNodeDefinitionsJSON(req.NodeInvoker.ScriptNodeFunctions())
		if err != nil {
			return nil, fmt.Errorf("encoding node functions: %w", err)
		}
		files["node_functions.json"] = definitions
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(scratch, name), []byte(content), 0o600); err != nil {
			return nil, fmt.Errorf("staging %s: %w", name, err)
		}
	}

	out := newConsole(req.Limits.MaxOutput)
	bridge := newHTTPBridge(req.Network, req.Limits)

	// The scratch directory is mounted without symbolic links; see scratchFS.
	mounts, ok := wazero.NewFSConfig().(sysfs.FSConfig)
	if !ok {
		return nil, errors.New("python runtime cannot confine its scratch directory")
	}
	fsConfig := mounts.
		WithSysFSMount(scratchFS{sysfs.DirFS(scratch)}, "/karakuri").
		WithFSMount(&rpcFS{ctx: ctx, bridge: bridge, scratch: scratch, invoker: req.NodeInvoker}, "/rpc")

	config := wazero.NewModuleConfig().
		WithStdout(out).
		WithStderr(out).
		WithArgs("python", "/karakuri/main.py").
		WithEnv("PYTHONHOME", "/usr/local").
		WithEnv("PYTHONPATH", "/karakuri").
		// Bytecode caching would write into the scratch mount for no benefit,
		// since the directory is discarded when the run ends.
		WithEnv("PYTHONDONTWRITEBYTECODE", "1").
		WithEnv("PYTHONUNBUFFERED", "1").
		WithFSConfig(fsConfig).
		WithStartFunctions("_start").
		WithName("")

	module, err := p.runtime.InstantiateModule(ctx, p.compiled, config)
	if module != nil {
		_ = module.Close(ctx)
	}
	if err != nil && !isPythonExit(err) {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("script timed out after %s", req.Limits.Timeout)
		}
		return nil, fmt.Errorf("python runtime error: %w", err)
	}

	// The wrapper reports success and failure through the output document;
	// a missing document means the interpreter died before it could run.
	data, readErr := os.ReadFile(filepath.Join(scratch, "output.json"))
	if readErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("script timed out after %s", req.Limits.Timeout)
		}
		return nil, fmt.Errorf("script produced no result: %s",
			strings.TrimSpace(strings.Join(out.snapshot(), "; ")))
	}
	var result pythonOutput
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("script result is not valid JSON: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("script raised:\n%s", strings.TrimSpace(result.Error))
	}
	return &Result{
		Value:     result.Value,
		Vars:      result.Vars,
		Flow:      result.Flow,
		Global:    result.Global,
		MsgWrites: result.MsgWrites,
		Output:    out.snapshot(),
	}, nil
}

// scratchFS is a script's scratch directory mount with symbolic links refused.
// The mount resolves paths on the host, so a relative link planted inside it
// would be followed out of the directory: the script could read any file the
// server can, and the host side, which reads and writes staged files by name,
// would follow one too.
type scratchFS struct {
	experimentalsys.FS
}

// Symlink refuses to create a link.
func (scratchFS) Symlink(oldPath, linkName string) experimentalsys.Errno {
	return experimentalsys.EPERM
}

// isPythonExit reports whether an instantiation error is just the interpreter
// exiting with status 0, which WASI surfaces as a sys.ExitError.
func isPythonExit(err error) bool {
	var exit *sys.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode() == 0
	}
	return false
}

// ---- RPC mount ----

// rpcFS is the synthetic read-only filesystem mounted at /rpc. Opening one of
// its files performs the request staged in the scratch directory and writes the
// response back before the guest's open returns, which makes a synchronous host
// call out of nothing but a file read. The invoker belongs to this per-run
// filesystem, never to the shared PythonRuntime.
type rpcFS struct {
	ctx     context.Context
	bridge  *httpBridge
	scratch string
	invoker nodeapi.ScriptNodeInvoker
}

// Open handles the available RPC files and the directory itself; nothing else exists.
func (r *rpcFS) Open(name string) (fs.File, error) {
	switch name {
	case ".":
		return &rpcDir{}, nil
	case "http":
		return r.performHTTP()
	case "node":
		if r.invoker == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return r.performNode()
	default:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
}

// performHTTP reads the staged request, performs it under policy, and stages
// the response for the guest to read back.
func (r *rpcFS) performHTTP() (fs.File, error) {
	staged, err := os.ReadFile(filepath.Join(r.scratch, "http_req.json"))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: "http", Err: fs.ErrInvalid}
	}
	var request httpRequest
	if err := json.Unmarshal(staged, &request); err != nil {
		return nil, &fs.PathError{Op: "open", Path: "http", Err: fs.ErrInvalid}
	}

	response := r.bridge.do(r.ctx, request)
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: "http", Err: fs.ErrInvalid}
	}
	if err := os.WriteFile(filepath.Join(r.scratch, "http_res.json"), encoded, 0o600); err != nil {
		return nil, &fs.PathError{Op: "open", Path: "http", Err: fs.ErrPermission}
	}
	return &rpcFile{name: "http", data: []byte("ok")}, nil
}

// rpcFile is a read-only in-memory file.
type rpcFile struct {
	name   string
	data   []byte
	offset int
}

func (f *rpcFile) Stat() (fs.FileInfo, error) {
	return &rpcFileInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *rpcFile) Read(p []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.offset:])
	f.offset += n
	return n, nil
}

func (f *rpcFile) Close() error { return nil }

// rpcDir represents the mount point itself so directory operations succeed.
type rpcDir struct{}

func (*rpcDir) Stat() (fs.FileInfo, error) {
	return &rpcFileInfo{name: ".", dir: true}, nil
}
func (*rpcDir) Read([]byte) (int, error) { return 0, io.EOF }
func (*rpcDir) Close() error             { return nil }
func (*rpcDir) ReadDir(int) ([]fs.DirEntry, error) {
	return nil, nil
}

// rpcFileInfo describes an rpc mount entry.
type rpcFileInfo struct {
	name string
	size int64
	dir  bool
}

func (i *rpcFileInfo) Name() string { return i.name }
func (i *rpcFileInfo) Size() int64  { return i.size }
func (i *rpcFileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i *rpcFileInfo) ModTime() time.Time { return time.Time{} }
func (i *rpcFileInfo) IsDir() bool        { return i.dir }
func (i *rpcFileInfo) Sys() any           { return nil }
