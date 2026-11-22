package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Reading and writing one file answers "what is in it"; these three answer
// "what is there" and "put it somewhere else", which a flow watching a drop
// folder needs before it can act on anything.

// Entry kinds a listing keeps.
const (
	listFiles       = "files"
	listDirectories = "directories"
	listBoth        = "both"
)

// Listing orders. Newest and largest first are what a flow picking one entry
// out of a folder asks for; name is the stable order for everything else.
const (
	sortName    = "name"
	sortNewest  = "newest"
	sortLargest = "largest"
)

// fileListNode lists a directory on the agent host. It is an output-only pure
// source like the readings: the listing is a look at the world with nothing
// changed by it, so a flow binds it to a config field and several consumers
// share one walk.
type fileListNode struct{}

func (fileListNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeFileList,
		Folder:            folder,
		Label:             "List files",
		Description:       "Lists a directory on this agent host, pulled when a consumer needs it",
		Icon:              "las la-folder-open",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "path",
				Label:       "Directory",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/spool/incoming",
			},
			{
				ID:          "pattern",
				Label:       "Name pattern",
				Type:        flow.FieldString,
				Placeholder: "*.csv",
				Help:        "Shell glob matched against each entry's name. Empty lists everything.",
			},
			{
				ID:      "include",
				Label:   "Include",
				Type:    flow.FieldSelect,
				Default: listFiles,
				Options: []nodeapi.Option{
					{Value: listFiles, Label: "Files"},
					{Value: listDirectories, Label: "Directories"},
					{Value: listBoth, Label: "Files and directories"},
				},
			},
			{
				ID:      "recursive",
				Label:   "Descend into subdirectories",
				Type:    flow.FieldBoolean,
				Default: false,
			},
			{
				ID:          "max_depth",
				Label:       "Depth limit",
				Type:        flow.FieldNumber,
				Placeholder: "3",
				ShowIf:      &nodeapi.ShowIf{Field: "recursive", Equals: true},
				Help:        "How many levels below the directory to walk. Empty walks all of them.",
			},
			{
				ID:      "hidden",
				Label:   "Include hidden entries",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Names starting with a dot, and everything inside a hidden directory.",
			},
			{
				ID:      "sort",
				Label:   "Order",
				Type:    flow.FieldSelect,
				Default: sortName,
				Options: []nodeapi.Option{
					{Value: sortName, Label: "By name"},
					{Value: sortNewest, Label: "Newest first"},
					{Value: sortLargest, Label: "Largest first"},
				},
			},
			{
				ID:          "limit",
				Label:       "Keep only",
				Type:        flow.FieldNumber,
				Placeholder: "50",
				Help:        "Number of entries kept after ordering. Empty keeps all of them.",
			},
			{
				ID:      "allow_missing",
				Label:   "Treat a missing directory as empty",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Reports an empty listing instead of failing, for a folder that is only there sometimes.",
			},
		},
	}
}

// fileListConfig is the decoded listing configuration.
type fileListConfig struct {
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	Include      string `json:"include"`
	Recursive    bool   `json:"recursive"`
	MaxDepth     int    `json:"max_depth"`
	Hidden       bool   `json:"hidden"`
	Sort         string `json:"sort"`
	Limit        int    `json:"limit"`
	AllowMissing bool   `json:"allow_missing"`
}

// Execute walks the directory once and reports what matched.
func (fileListNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg fileListConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	root, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}

	// Check the root before walking, so a typo reads as a missing directory
	// rather than an empty one.
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) && cfg.AllowMissing {
			return nodeapi.One("out", &nodeapi.Msg{
				Payload: map[string]any{"path": root, "count": 0, "truncated": false, "entries": []any{}},
				Meta:    map[string]any{"path": root, "directory_missing": true},
			}), nil
		}
		return nil, fmt.Errorf("cannot list %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is a file, not a directory", root)
	}
	if pattern := strings.TrimSpace(cfg.Pattern); pattern != "" {
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return nil, fmt.Errorf("invalid name pattern %q: %w", pattern, err)
		}
	}

	entries, err := walkListing(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	sortListing(entries, cfg.Sort)
	total := len(entries)
	if cfg.Limit > 0 && len(entries) > cfg.Limit {
		entries = entries[:cfg.Limit]
	}

	payload := make([]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, entry.value)
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: map[string]any{
			"path":      root,
			"count":     total,
			"truncated": total > len(entries),
			"entries":   payload,
		},
		Meta: map[string]any{"path": root, "count": total},
	}), nil
}

// listing is one matched entry, keeping the fields the ordering needs beside
// the payload so sorting does not read them back out of a map.
type listing struct {
	name     string
	size     int64
	modified time.Time
	value    map[string]any
}

// walkListing collects the entries under root that the configuration keeps.
// A directory the agent's user cannot read is skipped rather than failing the
// walk: a recursive listing of a home directory hits one sooner or later, and
// the entries above it are still the answer.
func walkListing(ctx context.Context, root string, cfg fileListConfig) ([]listing, error) {
	pattern := strings.TrimSpace(cfg.Pattern)
	include := cfg.Include
	if include == "" {
		include = listFiles
	}

	var entries []listing
	walked := walkRoot(root)
	err := filepath.WalkDir(walked, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		path = underRoot(root, walked, path)
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			if path == root {
				return err
			}
			return nil
		}
		if path == root {
			return nil
		}

		name := d.Name()
		hidden := strings.HasPrefix(name, ".")
		if hidden && !cfg.Hidden {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Depth is counted from the root, so a non-recursive listing keeps the
		// first level and descends no further. A directory at the limit is
		// still listed; it is only what is inside it that is out of reach.
		if d.IsDir() {
			if keepEntry(include, true) && matchName(pattern, name) {
				entries = appendListing(entries, path, d)
			}
			depth := listingDepth(root, path)
			if !cfg.Recursive || (cfg.MaxDepth > 0 && depth >= cfg.MaxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if !keepEntry(include, false) || !matchName(pattern, name) {
			return nil
		}
		entries = appendListing(entries, path, d)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", root, err)
	}
	return entries, nil
}

// walkRoot returns the directory to hand filepath.WalkDir for root. WalkDir
// does not descend into a root that is itself a symlink, which is how /tmp,
// /var, and /etc are named on macOS, so a linked root is walked through the
// directory it names. Any other root is walked as given.
func walkRoot(root string) string {
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return root
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return resolved
}

// underRoot spells a path found while walking walked as a path under root, so
// what is reported keeps the name the node was configured with.
func underRoot(root, walked, path string) string {
	if walked == root {
		return path
	}
	if path == walked {
		return root
	}
	rel, err := filepath.Rel(walked, path)
	if err != nil {
		return path
	}
	return filepath.Join(root, rel)
}

// appendListing adds one entry, shaping it into the payload map. An entry that
// disappeared between the walk and the stat is dropped: a spool directory
// changes under a listing all the time.
func appendListing(entries []listing, path string, d fs.DirEntry) []listing {
	info, err := d.Info()
	if err != nil {
		return entries
	}
	return append(entries, listing{
		name:     d.Name(),
		size:     info.Size(),
		modified: info.ModTime(),
		value: map[string]any{
			"name":      d.Name(),
			"path":      path,
			"directory": d.IsDir(),
			"size":      info.Size(),
			"modified":  info.ModTime().UTC().Format(time.RFC3339),
			"mode":      info.Mode().Perm().String(),
		},
	})
}

// listingDepth reports how many levels below the root a path sits, with the
// entries directly inside it at depth 1.
func listingDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 1
	}
	return len(strings.Split(rel, string(os.PathSeparator)))
}

// keepEntry reports whether an entry of this kind is in the listing.
func keepEntry(include string, isDir bool) bool {
	switch include {
	case listDirectories:
		return isDir
	case listBoth:
		return true
	default:
		return !isDir
	}
}

// matchName applies the configured glob, treating an empty pattern as a match.
func matchName(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// sortListing orders the entries. Name is compared case-insensitively, which
// is the order a file manager shows and the one an author expects to read.
func sortListing(entries []listing, order string) {
	switch order {
	case sortNewest:
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].modified.After(entries[j].modified)
		})
	case sortLargest:
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].size > entries[j].size })
	default:
		sort.SliceStable(entries, func(i, j int) bool {
			return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
		})
	}
}

// fileCopyNode copies a file or directory tree on the agent host.
type fileCopyNode struct{}

func (fileCopyNode) Meta() nodeapi.NodeMeta {
	return transferMeta(nodeFileCopy, "Copy file", "las la-copy",
		"Copies a file or directory on this agent host")
}

// Execute copies the source onto the destination.
func (fileCopyNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	return runTransfer(ctx, ec, inputs, false)
}

// fileMoveNode moves or renames a file or directory tree on the agent host.
type fileMoveNode struct{}

func (fileMoveNode) Meta() nodeapi.NodeMeta {
	return transferMeta(nodeFileMove, "Move file", "las la-file-import",
		"Moves or renames a file or directory on this agent host")
}

// Execute moves the source onto the destination.
func (fileMoveNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	return runTransfer(ctx, ec, inputs, true)
}

// transferMeta builds the metadata the copy and move nodes share. They are two
// node types rather than one with a direction, because the palette is where an
// author looks for the verb and neither reads well as a mode of the other.
func transferMeta(nodeType, label, icon, description string) nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeType,
		Folder:      folder,
		Label:       label,
		Description: description,
		Icon:        icon,
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "source",
				Label:       "Source",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/spool/incoming/{{ payload.name }}",
				Help:        "{{ }} segments are rendered against the message.",
			},
			{
				ID:          "destination",
				Label:       "Destination",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/var/spool/done/",
				Help: "Target path. A path ending in a separator, or naming an existing directory, " +
					"puts the source inside it under its own name.",
			},
			{
				ID:      "overwrite",
				Label:   "Replace an existing destination",
				Type:    flow.FieldBoolean,
				Default: false,
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

// transferConfig is the decoded copy or move configuration.
type transferConfig struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite"`
	CreateDirs  *bool  `json:"create_dirs"`
}

// runTransfer performs a copy or a move and passes the message through with
// both paths in metadata.
func runTransfer(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg, remove bool) (map[string][]*nodeapi.Msg, error) {
	var cfg transferConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	source, err := agentnode.Path(ec, cfg.Source, msg)
	if err != nil {
		return nil, err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", source, err)
	}

	// Resolve the destination before touching anything, since a directory
	// target means the source keeps its own name inside it.
	rawDestination, err := agentnode.Interpolate(ec, cfg.Destination, msg)
	if err != nil {
		return nil, err
	}
	destination, err := resolveDestination(rawDestination, source)
	if err != nil {
		return nil, err
	}
	// Compare where the paths lead rather than how they are spelled. Through
	// a symlinked directory the same file has two names, and a replacing move
	// would copy it over itself and then delete it as the source.
	realSource, realDestination := resolvedParent(source), resolvedParent(destination)
	if realDestination == realSource {
		return nil, fmt.Errorf("%s is both the source and the destination", source)
	}
	if sourceInfo.IsDir() && strings.HasPrefix(realDestination+string(os.PathSeparator), realSource+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%s is inside %s, which would copy the tree into itself", destination, source)
	}

	if cfg.CreateDirs == nil || *cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(destination), fileDirPerm); err != nil {
			return nil, fmt.Errorf("cannot create the directory for %s: %w", destination, err)
		}
	}

	// A move with no destination to preserve is a rename when both paths are on
	// one filesystem. Existing destinations and cross-device moves take the
	// staged copy path below so failure cannot destroy the prior destination.
	if remove {
		if _, err := os.Lstat(destination); os.IsNotExist(err) {
			if err := os.Rename(source, destination); err == nil {
				out := agentnode.PassThrough(msg, map[string]any{"source": source, "destination": destination, "moved": true})
				return nodeapi.One("out", out), nil
			}
		}
	}

	staged, err := stagingPath(destination, "transfer")
	if err != nil {
		return nil, fmt.Errorf("reserving a staging path for %s: %w", destination, err)
	}
	defer removeTree(staged)
	written, err := copyPath(ctx, source, staged)
	if err != nil {
		return nil, err
	}
	if err := commitReplacement(staged, destination, cfg.Overwrite); err != nil {
		return nil, err
	}
	if remove {
		if err := os.RemoveAll(source); err != nil {
			return nil, fmt.Errorf("copied to %s but cannot remove %s: %w", destination, source, err)
		}
	}

	out := agentnode.PassThrough(msg, map[string]any{
		"source":      source,
		"destination": destination,
		"bytes":       written,
		"moved":       remove,
	})
	return nodeapi.One("out", out), nil
}

// resolveDestination expands a destination that names a directory into the
// full path the source lands at.
func resolveDestination(raw, source string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("no destination configured")
	}
	intoDirectory := strings.HasSuffix(trimmed, string(os.PathSeparator)) || trimmed == "."
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid destination %q: %w", raw, err)
	}
	abs = filepath.Clean(abs)
	if !intoDirectory {
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			intoDirectory = true
		}
	}
	if intoDirectory {
		return filepath.Join(abs, filepath.Base(source)), nil
	}
	return abs, nil
}

// resolvedParent returns path with the symlinks in its directory resolved. The
// last element is left alone because a transfer acts on a link itself, not on
// its target. Directories that do not exist yet are kept as written under the
// nearest ancestor that does.
func resolvedParent(path string) string {
	dir, rest := filepath.Dir(path), filepath.Base(path)
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir, rest = parent, filepath.Join(filepath.Base(dir), rest)
	}
}

// copyPath copies a file, a symlink, or a whole tree, and reports the bytes
// written. Permissions follow the source; ownership does not, because the
// agent's user is rarely allowed to give a file away.
func copyPath(ctx context.Context, source, destination string) (int64, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", source, err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(source)
		if err != nil {
			return 0, fmt.Errorf("cannot read the link %s: %w", source, err)
		}
		if err := os.Symlink(target, destination); err != nil {
			return 0, fmt.Errorf("cannot recreate the link %s: %w", destination, err)
		}
		return 0, nil
	case info.IsDir():
		return copyTree(ctx, source, destination, info)
	case !info.Mode().IsRegular():
		// Opening a FIFO blocks until a writer appears and a device has no
		// end to read to, so neither can be copied as content.
		return 0, fmt.Errorf("cannot copy %s: it is not a regular file, directory, or link", source)
	default:
		return copyFile(source, destination, info)
	}
}

// copyTree copies a directory and everything under it. The directory is
// created owner-writable and takes the source's mode only once its children
// are in place, so a read-only source directory can still be filled.
func copyTree(ctx context.Context, source, destination string, info os.FileInfo) (int64, error) {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return 0, fmt.Errorf("cannot create %s: %w", destination, err)
	}
	children, err := os.ReadDir(source)
	if err != nil {
		return 0, fmt.Errorf("cannot list %s: %w", source, err)
	}
	var total int64
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		written, err := copyPath(ctx, filepath.Join(source, child.Name()), filepath.Join(destination, child.Name()))
		total += written
		if err != nil {
			return total, err
		}
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return total, fmt.Errorf("cannot set the mode of %s: %w", destination, err)
	}
	return total, nil
}

// copyFile copies one file's contents.
func copyFile(source, destination string, info os.FileInfo) (int64, error) {
	in, err := os.Open(source)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", source, err)
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return 0, fmt.Errorf("cannot write %s: %w", destination, err)
	}
	// The umask clears bits from the mode a file is created with, so the
	// source's mode is applied again to the open file.
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		out.Close()
		return 0, fmt.Errorf("cannot set the mode of %s: %w", destination, err)
	}
	written, err := io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return written, fmt.Errorf("cannot copy %s to %s: %w", source, destination, err)
	}
	return written, nil
}
