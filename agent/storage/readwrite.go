package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// File node defaults. The read cap keeps one oversized file from filling the
// agent's memory and the gRPC message it would ride home in; operators raise it
// with --max-output, which is the same budget the process nodes spend.
const (
	fileDirPerm = 0o755
	filePerm    = 0o644
)

// File content formats the read node produces.
const (
	fileFormatText   = "text"
	fileFormatLines  = "lines"
	fileFormatBinary = "binary"
)

// File write modes.
const (
	fileModeOverwrite = "overwrite"
	fileModeAppend    = "append"
	fileModeDelete    = "delete"
	fileModeCreate    = "create"
)

// Node type names this group contributes. They are the names an operator
// writes in the disable list and the names the host namespaces as
// agent.<id>.<name>, so they are constants rather than literals in the metadata.
const (
	nodeFileRead  = "file-read"
	nodeFileStat  = "file-stat"
	nodeFileWrite = "file-write"
	nodeFileList  = "file-list"
	nodeFileCopy  = "file-copy"
	nodeFileMove  = "file-move"
	nodeFileWatch = "file-watch"
	nodeLogTail   = "log-tail"
)

// fileStatNode reports metadata without loading the file into a message. A
// SHA-256 digest is optional because hashing still has to read the whole file.
type fileStatNode struct{}

func (fileStatNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeFileStat,
		Folder:            folder,
		Label:             "File details",
		Description:       "Reports a path's type, size, timestamps, permissions, and optional SHA-256 digest",
		Icon:              "las la-file-alt",
		Retryable:         true,
		Pure:              true,
		TestWithoutParams: true,
		Outputs:           []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []nodeapi.FieldSpec{
			{ID: "path", Label: "Path", Type: flow.FieldString, Required: true, Placeholder: "/srv/archive/report.json"},
			{
				ID:      "sha256",
				Label:   "Calculate SHA-256",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Reads the entire file to calculate its digest. Directories cannot be hashed.",
			},
			{
				ID:      "allow_missing",
				Label:   "Allow a missing path",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Reports exists as false instead of failing when the path is absent.",
			},
		},
	}
}

type fileStatConfig struct {
	Path         string `json:"path"`
	SHA256       bool   `json:"sha256"`
	AllowMissing bool   `json:"allow_missing"`
}

func (fileStatNode) Execute(_ context.Context, ec nodeapi.ExecContext, _ map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg fileStatConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}
	payload, err := statPath(path, cfg.SHA256)
	if err != nil {
		if os.IsNotExist(err) && cfg.AllowMissing {
			payload = map[string]any{"path": path, "exists": false}
		} else {
			return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
		}
	}
	return nodeapi.One("out", &nodeapi.Msg{Payload: payload, Topic: path, Meta: map[string]any{"source": "file-stat"}}), nil
}

func statPath(path string, withDigest bool) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	kind := "file"
	switch {
	case info.IsDir():
		kind = "directory"
	case !info.Mode().IsRegular():
		kind = "other"
	}
	out := map[string]any{
		"path": path, "exists": true, "type": kind, "size": info.Size(),
		"modified": info.ModTime().UTC().Format(time.RFC3339Nano),
		"mode":     info.Mode().String(), "permissions": fmt.Sprintf("%04o", info.Mode().Perm()),
	}
	if !withDigest {
		return out, nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	out["sha256"] = fmt.Sprintf("%x", hash.Sum(nil))
	return out, nil
}

// fileReadNode reads a file on the agent host. Unlike the server's own read
// node it is confined by nothing but the account the agent runs as: that is the
// point of putting an agent on a machine, and it is why the answer to "this
// agent may only touch /srv" is a user account, not a config key. It is an
// output-only value source, pulled when a consuming node needs the content and
// memoized per run, so several consumers share one read.
type fileReadNode struct {
	opts agentnode.Options
}

func (fileReadNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeFileRead,
		Folder:            folder,
		Label:             "Read file",
		Description:       "Reads a file on this agent host as text, lines, or binary, pulled when a consumer needs it",
		Icon:              "las la-file-import",
		Retryable:         true,
		Pure:              true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{
				ID: "out", Label: "Out", Type: flow.PortString,
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{
						{Match: map[string][]string{"format": {fileFormatLines}}, Type: flow.PortObject},
						{Match: map[string][]string{"format": {fileFormatBinary}}, Type: flow.PortBinary},
					},
					Default: flow.PortString,
				},
			},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/log/syslog",
				Help: "File to read, as the agent's own user sees it. Wire the field to build the " +
					"path from another node, or use {{ }} segments to read flow and global variables.",
			},
			{
				ID:      "format",
				Label:   "Read as",
				Type:    flow.FieldSelect,
				Default: fileFormatText,
				Options: []nodeapi.Option{
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

// fileReadConfig is the decoded read configuration.
type fileReadConfig struct {
	Path         string `json:"path"`
	Format       string `json:"format"`
	AllowMissing bool   `json:"allow_missing"`
}

// Execute reads the file and emits its content as the payload.
func (n *fileReadNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg fileReadConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}

	// Read it, treating a missing file as a valid outcome when configured.
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && cfg.AllowMissing {
			return nodeapi.One("out", &nodeapi.Msg{
				Meta: map[string]any{"path": path, "file_missing": true},
			}), nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", path)
	}
	// A device, a FIFO, or a /proc entry reports no useful size: /dev/zero
	// would be read until the agent was out of memory and a FIFO would block
	// the read for good. Only regular files are read, and the read itself is
	// capped rather than trusting the size the stat reported.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	limit := int64(agentnode.ReadLimit(n.opts.MaxOutput))
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit; raise --max-output on the agent",
			path, info.Size(), limit)
	}
	data, err := readFileCapped(path, limit)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	// Shape the content into the payload.
	var content any
	switch cfg.Format {
	case fileFormatText, "":
		content = string(data)
	case fileFormatBinary:
		content = data
	case fileFormatLines:
		content = agentnode.TextLines(string(data))
	default:
		return nil, fmt.Errorf("unknown read format %q", cfg.Format)
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: content,
		Meta: map[string]any{
			"path":     path,
			"size":     len(data),
			"modified": info.ModTime().Format(time.RFC3339),
		},
	}), nil
}

// fileWriteNode writes message content to a file on the agent host.
type fileWriteNode struct{}

func (fileWriteNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeFileWrite,
		Folder:      folder,
		Label:       "Write file",
		Description: "Writes message content to a file on this agent host, overwriting, appending, or deleting it",
		Icon:        "las la-file-export",
		Inputs: []nodeapi.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortAny,
			TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}},
		}},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/tmp/report.json",
				Help:        "File to write. {{ }} segments are supported, so the path can come from the message.",
			},
			{
				ID:      "mode",
				Label:   "Mode",
				Type:    flow.FieldSelect,
				Default: fileModeOverwrite,
				Options: []nodeapi.Option{
					{Value: fileModeOverwrite, Label: "Replace the file"},
					{Value: fileModeCreate, Label: "Create only (fail if path exists)"},
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
					"they are; objects and arrays are written as JSON. Ignored when deleting.",
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
			{ID: "preserve_permissions", Label: "Preserve existing permissions", Type: flow.FieldBoolean, Default: true, ShowIf: &nodeapi.ShowIf{Field: "mode", Equals: fileModeOverwrite}, Help: "Off applies the normal new-file permissions during replacement."},
			{ID: "expected_sha256", Label: "Expected previous SHA-256", Type: flow.FieldString, ShowIf: &nodeapi.ShowIf{Field: "mode", Equals: fileModeOverwrite}, Help: "Optional digest of an existing regular file. Checked before staging and installation; external writers can still race the final check. Supports {{ }} expressions."},
		},
	}
}

// fileWriteConfig is the decoded write configuration. Newline and CreateDirs
// are pointers so an absent field keeps its default of on.
type fileWriteConfig struct {
	Path                string `json:"path"`
	Mode                string `json:"mode"`
	Property            string `json:"property"`
	Newline             *bool  `json:"newline"`
	CreateDirs          *bool  `json:"create_dirs"`
	PreservePermissions *bool  `json:"preserve_permissions"`
	ExpectedSHA256      string `json:"expected_sha256"`
}

// Execute writes, appends to, or deletes the file and passes the message
// through with the outcome in metadata.
func (fileWriteNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg fileWriteConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	if cfg.Mode != "" && cfg.Mode != fileModeOverwrite && cfg.Mode != fileModeCreate && cfg.Mode != fileModeAppend && cfg.Mode != fileModeDelete {
		return nil, fmt.Errorf("unknown write mode %q", cfg.Mode)
	}
	expected, err := agentnode.Interpolate(ec, cfg.ExpectedSHA256, msg)
	if err != nil {
		return nil, err
	}
	if expected != "" && cfg.Mode != "" && cfg.Mode != fileModeOverwrite {
		return nil, fmt.Errorf("expected SHA-256 is only available for replacement")
	}
	if err := validateWriteDigest(expected); err != nil {
		return nil, err
	}
	path, err := agentnode.Path(ec, cfg.Path, msg)
	if err != nil {
		return nil, err
	}
	out := agentnode.PassThrough(msg, map[string]any{"path": path})

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
		out.Meta["deleted"] = true
		return nodeapi.One("out", out), nil
	}

	// Render the content.
	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := agentnode.ResolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}
	data := agentnode.ValueBytes(value)
	if _, isBytes := value.([]byte); !isBytes {
		// A trailing newline is what makes appended lines readable. Binary is
		// left alone: bytes are not lines, and one appended would corrupt them.
		if (cfg.Newline == nil || *cfg.Newline) && !strings.HasSuffix(string(data), "\n") {
			data = append(data, '\n')
		}
	}
	if cfg.CreateDirs == nil || *cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(path), fileDirPerm); err != nil {
			return nil, fmt.Errorf("cannot create the directory for %s: %w", path, err)
		}
	}

	// Write it.
	switch cfg.Mode {
	case "", fileModeOverwrite:
		if err := writeGuarded(ctx, path, data, filePerm, cfg.PreservePermissions == nil || *cfg.PreservePermissions, false, expected); err != nil {
			return nil, fmt.Errorf("cannot write %s: %w", path, err)
		}
	case fileModeCreate:
		if err := writeGuarded(ctx, path, data, filePerm, false, true, ""); err != nil {
			return nil, err
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

	out.Meta["bytes"] = len(data)
	return nodeapi.One("out", out), nil
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
		return nil, fmt.Errorf("over the %d byte limit; raise --max-output on the agent", limit)
	}
	return data, nil
}
