package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// File node defaults. The read cap keeps one oversized file from taking the
// process down with it; operators raise it through storage.max_read_size.
const (
	fileDefaultMaxRead = 8 << 20
	fileDirPerm        = 0o755
	filePerm           = 0o644
)

// FileReadNode reads a file from the host filesystem. It is an output-only
// value source, like Value: it never fires on its own, and it is pulled when a
// consuming node needs the content — memoized per run, so several consumers
// share one read. A failure surfaces at the node that pulled it.
type FileReadNode struct{}

func (FileReadNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "file-read",
		Label:             "Read File",
		Folder:            flow.FolderStorage,
		Description:       "Reads a file as text, lines, or binary, pulled when a consumer needs it",
		Icon:              "las la-file-import",
		Retryable:         true,
		Pure:              true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{
				ID: "out", Label: "Out", Type: flow.PortString,
				// Text is a string; lines is an array; binary is bytes.
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{
						{Match: map[string][]string{"format": {fileFormatLines}}, Type: flow.PortObject},
						{Match: map[string][]string{"format": {fileFormatBinary}}, Type: flow.PortBinary},
					},
					Default: flow.PortString,
				},
			},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/lib/karakuri/files/report.json",
				Help: "File to read. Wire the field to build the path from another node, or use " +
					"{{ }} segments to read flow and global variables.",
			},
			{
				ID:      "format",
				Label:   "Read as",
				Type:    flow.FieldSelect,
				Default: fileFormatText,
				Options: []engine.Option{
					{Value: fileFormatText, Label: "Text, as one string"},
					{Value: fileFormatLines, Label: "Text, as an array of lines"},
					{Value: fileFormatBinary, Label: "Binary"},
				},
				Help: "Parse structured text with a json, csv, or yaml node after this one, and " +
					"turn binary into text with a base64 encode node.",
			},
			{
				ID:      "allow_missing",
				Label:   "Allow a missing file",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Emits null instead of failing when the file does not exist, with " +
					"meta.file_missing set so a switch can tell it from an empty file.",
			},
		},
	}
}

// File content formats the read node produces.
const (
	fileFormatText  = "text"
	fileFormatLines = "lines"
	// fileFormatBinary hands the bytes downstream as they are. Encoding them
	// as text is a base64 encode node's job, not a setting here: a format that
	// silently produced base64 was why a file read could not say what it
	// carried.
	fileFormatBinary = "binary"
)

// fileReadConfig is the decoded file-read node configuration.
type fileReadConfig struct {
	Path         string `json:"path"`
	Format       string `json:"format"`
	AllowMissing bool   `json:"allow_missing"`
}

// Execute reads the configured file and emits its content as the payload.
// The node takes no message inputs: it runs when pulled over a value edge, or
// as the seed of a test run.
func (FileReadNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg fileReadConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}

	// Resolve the path under the storage policy.
	path, err := resolveFilePath(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}

	// Read it, treating a missing file as a valid outcome when configured.
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && cfg.AllowMissing {
			return engine.One("out", &engine.Msg{
				Meta: map[string]any{"path": path, "file_missing": true},
			}), nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", path)
	}
	// A device, a FIFO, or a /proc entry reports no useful size: /dev/zero
	// would be read until memory ran out and a FIFO would block the worker for
	// good. Only regular files are read, and the read itself is capped rather
	// than trusting the size the stat reported.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	limit := maxFileReadSize()
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte read limit", path, info.Size(), limit)
	}
	data, err := readFileCapped(path, limit)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	ec.Logger().Debug("file read", zap.String("path", path), zap.Int("bytes", len(data)))

	// Shape the content into the payload.
	var content any
	switch format := cfg.Format; format {
	case fileFormatText, "":
		content = string(data)
	case fileFormatBinary:
		content = data
	case fileFormatLines:
		content = fileLines(string(data))
	default:
		return nil, fmt.Errorf("unknown read format %q", format)
	}
	return engine.One("out", &engine.Msg{
		Payload: content,
		Meta: map[string]any{
			"path":     path,
			"size":     len(data),
			"modified": info.ModTime().Format(time.RFC3339),
		},
	}), nil
}

// fileLines splits file content into lines, dropping the trailing empty line a
// final newline leaves behind and tolerating CRLF.
func fileLines(text string) []any {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		// An empty slice encodes as [], where nil would encode as null.
		return []any{}
	}
	parts := strings.Split(text, "\n")
	out := make([]any, 0, len(parts))
	for _, line := range parts {
		out = append(out, strings.TrimSuffix(line, "\r"))
	}
	return out
}

// FileWriteNode writes message content to a file on the host filesystem.
type FileWriteNode struct{}

func (FileWriteNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "file-write",
		Label:       "Write File",
		Folder:      flow.FolderStorage,
		Description: "Writes message content to a file, overwriting, appending, or deleting it",
		Icon:        "las la-file-export",
		Retryable:   true,
		// The content may be text, bytes, or a value serialized on the way out,
		// so the input stays undeclared; the rule only reports that the payload
		// is an object when the content is read from inside it.
		Inputs: []engine.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortAny,
			TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}},
		}},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/lib/karakuri/files/log.txt",
				Help:        "File to write. {{ }} segments are supported, so the path can come from the message.",
			},
			{
				ID:      "mode",
				Label:   "Mode",
				Type:    flow.FieldSelect,
				Default: fileModeOverwrite,
				Options: []engine.Option{
					{Value: fileModeOverwrite, Label: "Replace the file"},
					{Value: fileModeAppend, Label: "Append to the file"},
					{Value: fileModeDelete, Label: "Delete the file"},
				},
			},
			{
				ID:          "property",
				Label:       "Content",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload",
				Help: "Dot path to the value written. Binary is written as its bytes and strings as " +
					"they are; objects and arrays are written as JSON. Serialize another way with a " +
					"csv, xml, or yaml encode node first. Ignored when deleting.",
			},
			{
				ID:      "newline",
				Label:   "End with a newline",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Adds a trailing newline when the content does not already end with one. Off writes the bytes exactly.",
			},
			{
				ID:      "create_dirs",
				Label:   "Create missing directories",
				Type:    flow.FieldBoolean,
				Default: true,
			},
		},
	}
}

// File write modes.
const (
	fileModeOverwrite = "overwrite"
	fileModeAppend    = "append"
	fileModeDelete    = "delete"
)

// fileWriteConfig is the decoded file-write node configuration. Newline and
// CreateDirs are pointers so an absent field keeps its default of on.
type fileWriteConfig struct {
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	Property   string `json:"property"`
	Newline    *bool  `json:"newline"`
	CreateDirs *bool  `json:"create_dirs"`
}

// contentProperty returns the message path the content is read from.
func (c fileWriteConfig) contentProperty() string {
	if raw := strings.TrimSpace(c.Property); raw != "" {
		return raw
	}
	return "payload"
}

// Execute writes, appends to, or deletes the configured file and passes the
// message through with the outcome in meta.
func (FileWriteNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg fileWriteConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	// Resolve the path under the storage policy.
	path, err := resolveFilePath(ec, cfg.Path, msg)
	if err != nil {
		return nil, err
	}
	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	out.Meta["path"] = path

	// Deleting takes no content, and a file that is already gone is a success:
	// the requested end state holds either way.
	if cfg.Mode == fileModeDelete {
		// os.Remove would take an empty directory too, which "delete the file"
		// does not promise.
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			return nil, fmt.Errorf("%s is a directory, not a file", path)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("cannot delete %s: %w", path, err)
		}
		ec.Logger().Info("file deleted", zap.String("path", path))
		out.Meta["deleted"] = true
		return engine.One("out", out), nil
	}

	// Render the content.
	data, err := fileWriteBytes(cfg, msg)
	if err != nil {
		return nil, err
	}
	if cfg.CreateDirs == nil || *cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(path), fileDirPerm); err != nil {
			return nil, fmt.Errorf("cannot create the directory for %s: %w", path, err)
		}
	}

	// Write it.
	switch cfg.Mode {
	case "", fileModeOverwrite:
		if err := os.WriteFile(path, data, filePerm); err != nil {
			return nil, fmt.Errorf("cannot write %s: %w", path, err)
		}
	case fileModeAppend:
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
		if err != nil {
			return nil, fmt.Errorf("cannot open %s for appending: %w", path, err)
		}
		_, err = f.Write(data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("cannot append to %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unknown write mode %q", cfg.Mode)
	}
	ec.Logger().Debug("file written",
		zap.String("path", path), zap.String("mode", cfg.Mode), zap.Int("bytes", len(data)))

	out.Meta["bytes"] = len(data)
	return engine.One("out", out), nil
}

// fileWriteBytes renders the configured content property into the bytes that
// hit the disk.
func fileWriteBytes(cfg fileWriteConfig, msg *engine.Msg) ([]byte, error) {
	property := cfg.contentProperty()
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}

	var data []byte
	switch t := value.(type) {
	case string:
		data = []byte(t)
	case []byte:
		// Binary is written as it is, with no trailing newline: bytes are not
		// lines, and appending one would corrupt the file.
		return t, nil
	case nil:
		data = nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("cannot write %q as JSON: %w", property, err)
		}
		data = encoded
	}

	// A trailing newline is what makes appended lines readable, and text files
	// are expected to end with one.
	if (cfg.Newline == nil || *cfg.Newline) && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	return data, nil
}

// resolveFilePath interpolates a configured path, makes it absolute, and checks
// it against the storage roots policy.
func resolveFilePath(ec engine.ExecContext, raw string, msg *engine.Msg) (string, error) {
	rendered, err := ec.Interpolate(strings.TrimSpace(raw), msg)
	if err != nil {
		return "", fmt.Errorf("invalid path template: %w", err)
	}
	return checkFilePath(rendered)
}

// checkFilePath cleans a path and enforces the configured storage roots. The
// check fails closed: with no roots configured nothing is reachable, so an
// install that never set the policy confines its file nodes to the data
// directory the default names rather than handing them the whole disk.
func checkFilePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("no file path configured")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", raw, err)
	}
	abs = filepath.Clean(abs)

	roots := storageRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("the file nodes are disabled: storage.roots is empty, so no path is reachable")
	}
	resolved := resolveExisting(abs)
	for _, root := range roots {
		if withinRoot(resolved, resolveExisting(root)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %q is outside storage.roots (%s); add its directory, or set storage.roots to [\"/\"] to allow any path",
		abs, strings.Join(roots, ", "))
}

// storageRoots returns the configured roots as absolute paths, skipping blanks.
func storageRoots() []string {
	if config.C == nil {
		return nil
	}
	var roots []string
	for _, root := range config.C.Storage.Roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			roots = append(roots, filepath.Clean(abs))
		}
	}
	return roots
}

// maxFileReadSize returns the configured read ceiling in bytes.
func maxFileReadSize() int64 {
	if config.C != nil && config.C.Storage.MaxReadSize > 0 {
		return int64(config.C.Storage.MaxReadSize)
	}
	return fileDefaultMaxRead
}

// maxSymlinkHops bounds how many unresolved links resolveExisting follows.
const maxSymlinkHops = 40

// resolveExisting resolves symlinks as far down the path as it exists, so a
// file about to be created is still checked against where its parent really
// lives. Without this a symlink inside an allowed root would be a way out of it.
//
// A link whose target does not exist yet fails to resolve, but a write follows
// it and creates the target, so the walk continues from where the link points
// instead of treating the link as a file in its own directory. The hop limit
// ends a chain of links that loops, which no write can follow either.
func resolveExisting(path string) string {
	rest := ""
	cur := path
	hops := 0
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest)
		}
		if info, statErr := os.Lstat(cur); statErr == nil && info.Mode()&os.ModeSymlink != 0 && hops < maxSymlinkHops {
			target, linkErr := os.Readlink(cur)
			dir, dirErr := filepath.EvalSymlinks(filepath.Dir(cur))
			if linkErr == nil && dirErr == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(dir, target)
				}
				cur = filepath.Clean(target)
				hops++
				continue
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// withinRoot reports whether path is root itself or sits under it. The
// separator is only appended when the root does not already end in one, or the
// filesystem root written as "/" would be compared as "//" and match nothing,
// which is the documented way to lift the confinement.
func withinRoot(path, root string) bool {
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(path, root)
}

// readFileCapped reads a file, failing rather than growing past limit bytes.
func readFileCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("over the %d byte read limit", limit)
	}
	return data, nil
}
