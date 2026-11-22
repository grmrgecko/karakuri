package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// File watch defaults. Editors and rsync write in bursts, so events are held
// briefly and coalesced per path rather than starting a run per write syscall.
const (
	fileWatchDefaultSettle = 500 * time.Millisecond
	fileWatchMaxDirs       = 1000
)

// fileWatchNode starts a run when a file or directory on the agent host
// changes. Watching from the agent is what makes the trigger usable on a
// network share or a NAS: fsnotify reports what the local kernel sees, so a
// server watching a mounted export hears nothing the exporting host does.
//
// Directories are what the kernel reports on, so a watched file is followed by
// watching its parent and filtering: that is also what makes the node survive
// the write-to-temp-then-rename pattern most editors use.
type fileWatchNode struct{}

func (fileWatchNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeFileWatch,
		Folder:            folder,
		Label:             "Watch files",
		Description:       "Starts a run when a file or directory changes on this agent host",
		Icon:              "las la-folder-open",
		Trigger:           true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/srv/incoming",
				Help:        "File or directory to watch. A directory reports changes to the entries inside it.",
			},
			{
				ID:      "recursive",
				Label:   "Include subdirectories",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Watches the whole tree under the path, picking up directories created later.",
			},
			{
				ID:          "pattern",
				Label:       "Name pattern",
				Type:        flow.FieldString,
				Placeholder: "*.json",
				Help:        "Shell glob matched against the file name. Empty reports every entry.",
			},
			{ID: "on_create", Label: "On create", Type: flow.FieldBoolean, Default: true},
			{ID: "on_write", Label: "On write", Type: flow.FieldBoolean, Default: true},
			{ID: "on_delete", Label: "On delete", Type: flow.FieldBoolean, Default: true},
			{
				ID:      "on_rename",
				Label:   "On rename",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "A rename out of the watched directory; the new name arrives as a create when it lands in one.",
			},
			{ID: "on_chmod", Label: "On permission change", Type: flow.FieldBoolean, Default: false},
			{
				ID:          "settle",
				Label:       "Settle time",
				Type:        flow.FieldDuration,
				Default:     "500ms",
				Placeholder: "500ms",
				Help: "How long a path must stay quiet before its run starts. Programs write in " +
					"bursts, so this collapses one save into one run. Zero fires on every event.",
			},
		},
	}
}

// fileWatchConfig is the decoded watch configuration. The event flags are
// pointers so an absent field keeps the default the editor shows.
type fileWatchConfig struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
	Pattern   string `json:"pattern"`
	OnCreate  *bool  `json:"on_create"`
	OnWrite   *bool  `json:"on_write"`
	OnDelete  *bool  `json:"on_delete"`
	OnRename  *bool  `json:"on_rename"`
	OnChmod   *bool  `json:"on_chmod"`
	Settle    string `json:"settle"`
}

// wants reports whether an fsnotify operation should start a run.
func (c fileWatchConfig) wants(op fsnotify.Op) bool {
	enabled := func(flag *bool, def bool) bool {
		if flag == nil {
			return def
		}
		return *flag
	}
	switch {
	case op.Has(fsnotify.Create):
		return enabled(c.OnCreate, true)
	case op.Has(fsnotify.Write):
		return enabled(c.OnWrite, true)
	case op.Has(fsnotify.Remove):
		return enabled(c.OnDelete, true)
	case op.Has(fsnotify.Rename):
		return enabled(c.OnRename, false)
	case op.Has(fsnotify.Chmod):
		return enabled(c.OnChmod, false)
	}
	return false
}

// Execute passes the run's initial message through. Test runs arrive with an
// empty payload; those get a synthesized change message so downstream nodes see
// the deployed shape.
func (fileWatchNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	msg := inputs[nodeapi.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return nodeapi.One("out", msg), nil
	}
	var cfg fileWatchConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}
	// Name the path itself for a file, and a stand-in entry for a directory, so
	// a test run shows what the deployed trigger will send.
	sample := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		sample = filepath.Join(path, "example.txt")
	}
	out := fileWatchMsg(sample, "write")
	if msg != nil {
		out.ID = msg.ID
		for k, v := range msg.Meta {
			out.Meta[k] = v
		}
	}
	return nodeapi.One("out", out), nil
}

// Start watches the configured path and emits a message per settled change
// until the context is canceled.
func (fileWatchNode) Start(ctx context.Context, ec nodeapi.ExecContext, emit func(*nodeapi.Msg)) error {
	var cfg fileWatchConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return err
	}
	settle := fileWatchDefaultSettle
	if raw := strings.TrimSpace(cfg.Settle); raw != "" {
		settle, err = time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid settle time %q: %w", cfg.Settle, err)
		}
	}
	if cfg.Pattern != "" {
		if _, err := filepath.Match(cfg.Pattern, "probe"); err != nil {
			return fmt.Errorf("invalid name pattern %q: %w", cfg.Pattern, err)
		}
	}

	// Decide what the kernel is asked to watch: the directory holding a watched
	// file, or the directory itself and optionally its subdirectories.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot watch %s: %w", path, err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("cannot create a file watcher: %w", err)
	}
	defer watcher.Close()

	dir := path
	var only string
	if !info.IsDir() {
		dir = filepath.Dir(path)
		only = filepath.Base(path)
	}
	if err := addWatchDirs(watcher, dir, cfg.Recursive && only == ""); err != nil {
		return err
	}
	ec.Logger().Info("file watch trigger armed",
		zap.String("path", path), zap.Bool("recursive", cfg.Recursive),
		zap.String("pattern", cfg.Pattern), zap.Duration("settle", settle))

	return watchLoop(ctx, ec, watcher, cfg, only, settle, emit)
}

// pendingChange is one path waiting out its settle window: the operation it
// reports and when it may be emitted.
type pendingChange struct {
	op       fsnotify.Op
	deadline time.Time
}

// watchLoop translates raw filesystem events into emitted messages, holding
// each path for the settle window so a burst of writes starts one run. Waits go
// through the injected clock, the same as on the server.
func watchLoop(ctx context.Context, ec nodeapi.ExecContext, watcher *fsnotify.Watcher, cfg fileWatchConfig, only string, settle time.Duration, emit func(*nodeapi.Msg)) error {
	clock := ec.Clock()
	// Pending holds the paths still settling. The wait channel stays nil while
	// nothing is pending, so an idle watch blocks instead of ticking.
	pending := map[string]pendingChange{}
	var waitC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return nil

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// A watch error is usually one directory going away; log and keep
			// the remaining watches running.
			ec.Logger().Warn("file watch error", zap.Error(err))

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// New subdirectories join the watch so a recursive tree stays
			// covered as it grows. A symlink to a directory is not followed,
			// matching the walk that armed the watch.
			if cfg.Recursive && only == "" && event.Op.Has(fsnotify.Create) {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() {
					if err := addWatchDirs(watcher, event.Name, true); err != nil {
						ec.Logger().Warn("cannot watch new directory",
							zap.String("path", event.Name), zap.Error(err))
					}
				}
			}
			// Any event on a settling path restarts its window, including kinds
			// the node does not report: the path is not quiet yet. A write or
			// permission change after a create keeps the create, since a copied
			// file arrives as CREATE followed by WRITE.
			if change, ok := pending[event.Name]; ok && settle > 0 {
				change.deadline = clock.Now().Add(settle)
				keepCreate := change.op.Has(fsnotify.Create) && !event.Op.Has(fsnotify.Remove) && !event.Op.Has(fsnotify.Rename)
				if !keepCreate && fileWatchMatches(cfg, only, event) {
					change.op = event.Op
				}
				pending[event.Name] = change
				continue
			}
			if !fileWatchMatches(cfg, only, event) {
				continue
			}
			if settle <= 0 {
				emit(fileWatchMsg(event.Name, fileWatchOpName(event.Op)))
				continue
			}
			pending[event.Name] = pendingChange{op: event.Op, deadline: clock.Now().Add(settle)}
			if waitC == nil {
				waitC = clock.After(settle)
			}

		case <-waitC:
			waitC = nil
			// Emit whatever has gone quiet and wait again for the rest, which
			// is what keeps a busy path from firing while it is still changing.
			now := clock.Now()
			var next time.Duration
			for name, change := range pending {
				if change.deadline.After(now) {
					if wait := change.deadline.Sub(now); next == 0 || wait < next {
						next = wait
					}
					continue
				}
				delete(pending, name)
				emit(fileWatchMsg(name, fileWatchOpName(change.op)))
			}
			if len(pending) > 0 {
				waitC = clock.After(next)
			}
		}
	}
}

// fileWatchMatches reports whether an event passes the node's filters: the
// watched file name when one file is watched, the glob, and the event kinds.
func fileWatchMatches(cfg fileWatchConfig, only string, event fsnotify.Event) bool {
	base := filepath.Base(event.Name)
	if only != "" && base != only {
		return false
	}
	if cfg.Pattern != "" {
		if ok, err := filepath.Match(cfg.Pattern, base); err != nil || !ok {
			return false
		}
	}
	return cfg.wants(event.Op)
}

// addWatchDirs registers dir with the watcher, walking the tree when recursive.
// Unreadable subdirectories are skipped rather than failing the whole watch.
// A dir that is a symlink is walked through the directory it names, and its
// subdirectories are watched under the linked spelling, so events carry the
// path the node was configured with.
func addWatchDirs(watcher *fsnotify.Watcher, dir string, recursive bool) error {
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("cannot watch %s: %w", dir, err)
	}
	if !recursive {
		return nil
	}
	count := 1
	walked := walkRoot(dir)
	return filepath.WalkDir(walked, func(path string, d fs.DirEntry, err error) error {
		path = underRoot(dir, walked, path)
		if err != nil || !d.IsDir() || path == dir {
			return nil
		}
		if count >= fileWatchMaxDirs {
			return fmt.Errorf("watching %s recursively would exceed %d directories", dir, fileWatchMaxDirs)
		}
		if err := watcher.Add(path); err != nil {
			// A directory removed mid-walk is normal; anything else is worth
			// failing on, since a silent gap in coverage is worse.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("cannot watch %s: %w", path, err)
		}
		count++
		return nil
	})
}

// fileWatchOpName renders an fsnotify operation as the event name flows see.
func fileWatchOpName(op fsnotify.Op) string {
	switch {
	case op.Has(fsnotify.Create):
		return "create"
	case op.Has(fsnotify.Write):
		return "write"
	case op.Has(fsnotify.Remove):
		return "delete"
	case op.Has(fsnotify.Rename):
		return "rename"
	case op.Has(fsnotify.Chmod):
		return "chmod"
	default:
		return strings.ToLower(op.String())
	}
}

// fileWatchMsg builds the message emitted for one settled change.
func fileWatchMsg(path, event string) *nodeapi.Msg {
	payload := map[string]any{
		"path":  path,
		"name":  filepath.Base(path),
		"dir":   filepath.Dir(path),
		"event": event,
	}
	// Size and modification time are only knowable while the file is still
	// there, which a delete or a rename means it is not.
	if info, err := os.Stat(path); err == nil {
		payload["size"] = info.Size()
		payload["modified"] = info.ModTime().Format(time.RFC3339)
		payload["is_dir"] = info.IsDir()
	}
	return &nodeapi.Msg{
		Payload: payload,
		Topic:   path,
		Meta: map[string]any{
			"source": "file-watch",
			"path":   path,
			"event":  event,
		},
	}
}
