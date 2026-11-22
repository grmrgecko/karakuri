package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Log tail defaults. The poll interval is what a tail costs when the file is
// quiet: one stat, two fingerprint reads, and one read per interval, which is
// why it is a second rather than a tick.
const (
	logTailDefaultPoll       = time.Second
	logTailReadChunk         = 32 << 10
	logTailCheckpointWrite   = 15 * time.Second
	logTailCheckpointExpiry  = 14 * 24 * time.Hour
	logTailCheckpointStoreID = "sincedb-v1"
	logTailDefaultBatch      = 1000
	logTailFingerprintBytes  = 256
)

// Payload shapes the tail emits.
const (
	logTailOutputLine   = "line"
	logTailOutputGroups = "groups"
)

// logTailNode follows a file and starts a run per line, the way tail -F does:
// it survives truncation and rotation, and it waits for a file that is not
// there yet rather than failing to arm. It is the trigger that notices a
// service complaining without anything polling the service itself.
//
// Polling rather than fsnotify is deliberate. A rotated log means the name now
// points at a different inode, and finding that out on a schedule is one stat,
// while doing it on events means handling the create, the rename, and the
// writes that arrive in between.
type logTailNode struct {
	opts agentnode.Options
}

func (logTailNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeLogTail,
		Folder:            folder,
		Label:             "Tail log",
		Description:       "Starts a run for each new line written to a file on this agent host",
		Icon:              "las la-stream",
		Trigger:           true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{
				ID: "out", Label: "Out", Type: flow.PortString,
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{
						{Match: map[string][]string{"output": {logTailOutputGroups}}, Type: flow.PortObject},
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
				Placeholder: "/var/log/nginx/error.log",
				Help: "File to follow. A file that does not exist yet is waited for, and one that " +
					"is rotated or truncated is picked up again from the start of the new file.",
			},
			{
				ID:          "pattern",
				Label:       "Match pattern",
				Type:        flow.FieldString,
				Placeholder: `(?i)error|panic`,
				Help: "Go regular expression. Only matching lines start a run; empty matches every " +
					"line. Named groups such as (?P<level>\\w+) are reported in meta.groups.",
			},
			{
				ID:      "output",
				Label:   "Emit",
				Type:    flow.FieldSelect,
				Default: logTailOutputLine,
				Options: []nodeapi.Option{
					{Value: logTailOutputLine, Label: "The line, as a string"},
					{Value: logTailOutputGroups, Label: "The pattern's named groups, as an object"},
				},
				Help: "Groups needs a pattern with named groups; the whole line is still in meta.line.",
			},
			{
				ID:      "from_start",
				Label:   "Read the existing file first",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Starts a run for every line already in the file when the flow deploys. Off, " +
					"only lines written from then on are reported.",
			},
			{
				ID:          "poll",
				Label:       "Poll interval",
				Type:        flow.FieldDuration,
				Default:     "1s",
				Placeholder: "1s",
				Help:        "How often the file is checked for new lines and for rotation.",
			},
			{
				ID:          "checkpoint_interval",
				Label:       "Save position every",
				Type:        flow.FieldDuration,
				Default:     "15s",
				Placeholder: "15s",
				Help:        "How often the byte position is saved. A crash can replay lines read since the last save.",
			},
			{
				ID:          "checkpoint_expiry",
				Label:       "Forget position after",
				Type:        flow.FieldDuration,
				Default:     "336h",
				Placeholder: "336h",
				Help:        "An inactive checkpoint is discarded after this long so a reused file identity cannot resume at an unrelated offset.",
			},
			{
				ID:          "max_lines_per_poll",
				Label:       "Lines per poll",
				Type:        flow.FieldNumber,
				Default:     logTailDefaultBatch,
				Placeholder: "1000",
				Help:        "Limits each batch so a large backlog cannot monopolize the agent. Remaining lines are read on later polls.",
			},
		},
	}
}

// logTailConfig is the decoded tail configuration.
type logTailConfig struct {
	Path               string `json:"path"`
	Pattern            string `json:"pattern"`
	Output             string `json:"output"`
	FromStart          bool   `json:"from_start"`
	Poll               string `json:"poll"`
	CheckpointInterval string `json:"checkpoint_interval"`
	CheckpointExpiry   string `json:"checkpoint_expiry"`
	MaxLinesPerPoll    int    `json:"max_lines_per_poll"`
}

// Execute passes a real line through and synthesizes one for a test run, so an
// author can exercise the graph without waiting for the log to say something.
func (logTailNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	msg := inputs[nodeapi.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return nodeapi.One("out", msg), nil
	}
	var cfg logTailConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}
	pattern, err := compileTailPattern(cfg.Pattern)
	if err != nil {
		return nil, err
	}

	// Use the last line the file actually holds when there is one, since a
	// sample from the real log is worth more than an invented string.
	sample := lastMatchingLine(path, pattern)
	if sample == "" {
		sample = "sample log line"
	}
	out := logTailMsg(cfg, path, sample, pattern)
	// A sample the pattern does not match leaves nothing for a groups payload
	// to hold, and an empty test message teaches an author nothing about what
	// the deployed trigger sends. Name the groups instead.
	if groups, ok := out.Payload.(map[string]any); ok && len(groups) == 0 {
		out.Payload = emptyGroups(pattern)
	}
	if msg != nil {
		out.ID = msg.ID
		for k, v := range msg.Meta {
			out.Meta[k] = v
		}
	}
	return nodeapi.One("out", out), nil
}

// Start follows the file until the context is canceled.
func (n *logTailNode) Start(ctx context.Context, ec nodeapi.ExecContext, emit func(*nodeapi.Msg)) error {
	var cfg logTailConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	path, err := agentnode.Path(ec, cfg.Path, nil)
	if err != nil {
		return err
	}
	pattern, err := compileTailPattern(cfg.Pattern)
	if err != nil {
		return err
	}
	if cfg.Output == logTailOutputGroups && pattern == nil {
		return fmt.Errorf("emitting named groups needs a match pattern")
	}
	poll := logTailDefaultPoll
	if raw := strings.TrimSpace(cfg.Poll); raw != "" {
		if poll, err = time.ParseDuration(raw); err != nil {
			return fmt.Errorf("invalid poll interval %q: %w", cfg.Poll, err)
		}
	}
	if poll <= 0 {
		poll = logTailDefaultPoll
	}
	checkpointInterval, err := tailDuration(cfg.CheckpointInterval, logTailCheckpointWrite, "checkpoint interval")
	if err != nil {
		return err
	}
	checkpointExpiry, err := tailDuration(cfg.CheckpointExpiry, logTailCheckpointExpiry, "checkpoint expiry")
	if err != nil {
		return err
	}

	store := ec.DurableStore()
	clock := ec.Clock()
	now := clock.Now()
	checkpoint := loadTailCheckpoint(store, now, checkpointExpiry)
	lastActive := now
	if checkpoint != nil {
		lastActive, _ = time.Parse(time.RFC3339Nano, checkpoint.Active)
	}
	tail := &tailReader{
		path: path, fromStart: cfg.FromStart, maxLine: agentnode.ReadLimit(n.opts.MaxOutput),
		maxLines: cfg.MaxLinesPerPoll, checkpoint: checkpoint, lastActive: lastActive,
	}
	if tail.maxLines <= 0 {
		tail.maxLines = logTailDefaultBatch
	}
	defer func() {
		saveTailCheckpoint(store, tail.savedPosition(ec.Clock().Now()))
		tail.close()
	}()
	ec.Logger().Info("log tail trigger armed",
		zap.String("path", path), zap.Bool("from_start", cfg.FromStart), zap.Duration("poll", poll))

	nextCheckpoint := clock.Now().Add(checkpointInterval)
	for {
		lines, err := tail.read()
		if tail.dirty {
			tail.lastActive = clock.Now()
			tail.dirty = false
		}
		if err != nil {
			// A read error is usually the file being replaced under us. Log it
			// and try again on the next poll rather than dropping the trigger,
			// which would need a redeploy to come back.
			ec.Logger().Warn("log tail read failed", zap.String("path", path), zap.Error(err))
		}
		for _, line := range lines {
			if pattern != nil && !pattern.MatchString(line) {
				continue
			}
			emit(logTailMsg(cfg, path, line, pattern))
		}
		if !clock.Now().Before(nextCheckpoint) {
			saveTailCheckpoint(store, tail.savedPosition(clock.Now()))
			nextCheckpoint = clock.Now().Add(checkpointInterval)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-clock.After(poll):
		}
	}
}

// tailDuration parses a positive duration setting and applies its default.
func tailDuration(raw string, fallback time.Duration, name string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return d, nil
}

// tailCheckpoint is the durable equivalent of one Logstash sincedb record.
// Identity prevents a reused path from borrowing an unrelated byte offset;
// active bounds how long an identity is trusted before inode reuse is likely.
type tailCheckpoint struct {
	Identity string `json:"identity"`
	Offset   int64  `json:"offset"`
	// Fingerprint is fileFingerprint at Offset, detecting a file rewritten
	// in place while the agent was stopped.
	Fingerprint string `json:"fingerprint,omitempty"`
	Active      string `json:"active"`
	Path        string `json:"path"`
	Discarding  bool   `json:"discarding,omitempty"`
}

// loadTailCheckpoint reads and expires the durable record.
func loadTailCheckpoint(store nodeapi.NodeStore, now time.Time, expiry time.Duration) *tailCheckpoint {
	value, ok := store.Get(logTailCheckpointStoreID)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		store.Delete(logTailCheckpointStoreID)
		return nil
	}
	var checkpoint tailCheckpoint
	if json.Unmarshal(raw, &checkpoint) != nil || checkpoint.Identity == "" || checkpoint.Offset < 0 {
		store.Delete(logTailCheckpointStoreID)
		return nil
	}
	active, err := time.Parse(time.RFC3339Nano, checkpoint.Active)
	if err != nil || now.Sub(active) > expiry {
		store.Delete(logTailCheckpointStoreID)
		return nil
	}
	return &checkpoint
}

// saveTailCheckpoint stores a usable record and ignores a tail that has not
// seen a file yet.
func saveTailCheckpoint(store nodeapi.NodeStore, checkpoint *tailCheckpoint) {
	if checkpoint == nil || checkpoint.Identity == "" {
		return
	}
	store.Set(logTailCheckpointStoreID, checkpoint)
}

// compileTailPattern compiles the configured pattern, nil when there is none.
func compileTailPattern(raw string) (*regexp.Regexp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	pattern, err := regexp.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid match pattern %q: %w", raw, err)
	}
	return pattern, nil
}

// logTailMsg builds the message for one line. The line is always in metadata,
// so a flow reading named groups can still report what it matched on.
func logTailMsg(cfg logTailConfig, path, line string, pattern *regexp.Regexp) *nodeapi.Msg {
	groups := matchGroups(pattern, line)
	meta := map[string]any{
		"source": "log-tail",
		"path":   path,
		"line":   line,
	}
	if len(groups) > 0 {
		meta["groups"] = groups
	}
	var payload any = line
	if cfg.Output == logTailOutputGroups {
		// An empty object rather than a nil map: a pattern with no named
		// groups has nothing to report, which is not the same as no payload.
		if groups == nil {
			groups = map[string]any{}
		}
		payload = groups
	}
	return &nodeapi.Msg{Payload: payload, Topic: path, Meta: meta}
}

// emptyGroups names the pattern's capture groups with no values, which is the
// shape a test run shows when its sample line does not match.
func emptyGroups(pattern *regexp.Regexp) map[string]any {
	groups := map[string]any{}
	if pattern == nil {
		return groups
	}
	for _, name := range pattern.SubexpNames() {
		if name != "" {
			groups[name] = ""
		}
	}
	return groups
}

// matchGroups returns the pattern's named capture groups for a line.
func matchGroups(pattern *regexp.Regexp, line string) map[string]any {
	if pattern == nil {
		return nil
	}
	match := pattern.FindStringSubmatch(line)
	if match == nil {
		return nil
	}
	groups := map[string]any{}
	for i, name := range pattern.SubexpNames() {
		if name == "" || i >= len(match) {
			continue
		}
		groups[name] = match[i]
	}
	return groups
}

// lastMatchingLine returns the final line of the file that satisfies the
// pattern, for the sample a test run shows. It reads the whole file, which is
// acceptable for a one-off in the editor and not for the tail itself.
func lastMatchingLine(path string, pattern *regexp.Regexp) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSuffix(lines[i], "\r")
		if line == "" {
			continue
		}
		if pattern == nil || pattern.MatchString(line) {
			return line
		}
	}
	return ""
}

// tailReader follows one file across appends, truncation, and rotation. It
// holds a partial trailing line until its newline arrives, so a line is never
// split across two runs.
type tailReader struct {
	path      string
	fromStart bool
	maxLine   int
	maxLines  int

	file     *os.File
	id       os.FileInfo
	offset   int64
	partial  []byte
	identity string
	// fingerprint is fileFingerprint at offset, refreshed after each read.
	fingerprint string
	checkpoint  *tailCheckpoint
	lastActive  time.Time
	dirty       bool
	discarding  bool
	// opened is false until the path has been looked at once, whether or not
	// a file was there. Only that first look may skip existing content, which
	// is what lets a tail arm on a path the program that writes it has not
	// created yet and still report that program's first lines.
	opened bool
}

// read returns the complete lines written since the last call, at most
// maxLines of them across every file the poll touches. A missing file is not
// an error: it is a log that has not been created or is mid-rotation.
//
// The name is checked before the held file is drained, so a rotation is
// followed only after the old file has been read to its end. A rotated file
// that still produced bytes is drained again on the next poll, which covers a
// writer that has not reopened its log yet and a backlog larger than one batch.
func (t *tailReader) read() ([]string, error) {
	if t.file == nil {
		if err := t.open(); err != nil {
			return nil, err
		}
		if t.file == nil {
			return nil, nil
		}
	}

	info, statErr := os.Stat(t.path)
	if err := t.checkRewritten(); err != nil {
		return nil, err
	}
	lines, read, err := t.drain(t.maxLines)
	if err != nil || t.full(lines) {
		return lines, err
	}
	if statErr != nil || os.SameFile(info, t.id) || read > 0 {
		return lines, nil
	}

	// The name points at another file and the old one is quiet. Its trailing
	// line will never get a newline, so it is reported as it stands.
	lines = append(lines, t.flushPartial()...)
	t.close()
	if err := t.open(); err != nil || t.file == nil {
		return lines, err
	}
	if t.full(lines) {
		return lines, nil
	}
	rest, _, err := t.drain(t.remaining(lines))
	return append(lines, rest...), err
}

// full reports whether a poll has used its line budget.
func (t *tailReader) full(lines []string) bool {
	return t.maxLines > 0 && len(lines) >= t.maxLines
}

// remaining is the budget left after lines, zero meaning unlimited.
func (t *tailReader) remaining(lines []string) int {
	if t.maxLines <= 0 {
		return 0
	}
	return t.maxLines - len(lines)
}

// checkRewritten starts the held file again from the beginning when the bytes
// before the offset are no longer the ones that were read. That is what
// truncation looks like (logrotate copytruncate), including a file that has
// grown past the old offset again before this poll, and what an in-place
// rewrite looks like.
func (t *tailReader) checkRewritten() error {
	if t.offset == 0 {
		return nil
	}
	sum, err := fileFingerprint(t.file, t.offset)
	if err == nil && sum == t.fingerprint {
		return nil
	}
	if _, err := t.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	t.offset = 0
	t.partial = nil
	t.discarding = false
	t.fingerprint = ""
	t.dirty = true
	return nil
}

// flushPartial reports a held line that will not be completed.
func (t *tailReader) flushPartial() []string {
	defer func() {
		t.partial = nil
		t.discarding = false
	}()
	if t.discarding || len(t.partial) == 0 {
		return nil
	}
	line := string(t.partial)
	if t.maxLine > 0 && len(line) > t.maxLine {
		line = agentnode.CapText(line, t.maxLine)
	}
	return []string{strings.TrimSuffix(line, "\r")}
}

// open attaches to the file, starting at the end unless the node asked for the
// existing content. A file that appears later is picked up from its start, so
// nothing written before the tail noticed it is missed. The startup checkpoint
// applies to the first attach only.
func (t *tailReader) open() error {
	file, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			// The path has now been looked at, so whatever appears there
			// later is new content and is read whole.
			t.opened = true
			return nil
		}
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	identity, err := fileIdentity(file, info)
	if err != nil {
		file.Close()
		return err
	}
	checkpoint := t.checkpoint
	t.checkpoint = nil
	offset := int64(0)
	t.discarding = false
	switch {
	case checkpoint != nil && checkpoint.Identity == identity:
		offset = checkpoint.Offset
		if offset > info.Size() {
			offset = 0
		}
		// A file truncated and refilled while the agent was stopped keeps its
		// identity, so the bytes before the offset decide whether it is the
		// same content. An older record without a fingerprint is trusted.
		if offset > 0 && checkpoint.Fingerprint != "" {
			if sum, err := fileFingerprint(file, offset); err != nil || sum != checkpoint.Fingerprint {
				offset = 0
			}
		}
		if offset > 0 {
			t.discarding = checkpoint.Discarding
		}
	case checkpoint != nil && checkpoint.Path == t.path:
		// The configured name now identifies a new file. Treat it as a rotation
		// and read from the beginning, including content written while offline.
	case !t.fromStart && !t.opened:
		// Only the first attach skips what is already there. After a rotation
		// the new file is read whole, since its lines are new.
		offset = info.Size()
	}
	fingerprint := ""
	if offset > 0 {
		if fingerprint, err = fileFingerprint(file, offset); err != nil {
			file.Close()
			return err
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return err
	}
	t.file = file
	t.id = info
	t.identity = identity
	t.offset = offset
	t.fingerprint = fingerprint
	t.opened = true
	t.partial = nil
	return nil
}

// savedPosition returns the last complete-line boundary. Saving the raw file
// offset would lose a partial line when the agent restarts before its newline.
func (t *tailReader) savedPosition(now time.Time) *tailCheckpoint {
	if t.identity == "" {
		return nil
	}
	offset := t.offset - int64(len(t.partial))
	if offset < 0 {
		offset = 0
	}
	if t.lastActive.IsZero() {
		t.lastActive = now
	}
	discarding := t.discarding
	fingerprint := ""
	if t.file != nil && t.offset > 0 {
		// The fingerprint below is taken from the file as it is now. A file
		// truncated and refilled past the offset since the last poll would
		// get a fingerprint of its new content, and the restart would resume
		// in the middle of it. What was read no longer matches, so the
		// position is the beginning.
		if sum, err := fileFingerprint(t.file, t.offset); err != nil || sum != t.fingerprint {
			offset = 0
			discarding = false
		}
	}
	if t.file != nil && offset > 0 {
		fingerprint, _ = fileFingerprint(t.file, offset)
	}
	return &tailCheckpoint{
		Identity:    t.identity,
		Offset:      offset,
		Fingerprint: fingerprint,
		Active:      t.lastActive.UTC().Format(time.RFC3339Nano),
		Path:        t.path,
		Discarding:  discarding,
	}
}

// fileFingerprint hashes the first and last logTailFingerprintBytes before
// offset. Appending never changes them, and truncating or rewriting the file
// almost always does, because log lines carry timestamps.
func fileFingerprint(file *os.File, offset int64) (string, error) {
	n := min(offset, logTailFingerprintBytes)
	buf := make([]byte, 2*n)
	if _, err := file.ReadAt(buf[:n], 0); err != nil {
		return "", err
	}
	if _, err := file.ReadAt(buf[n:], offset-n); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// drain reads what is available, up to limit complete lines when limit is
// positive, and reports how many bytes it read.
func (t *tailReader) drain(limit int) ([]string, int64, error) {
	var read int64
	defer func() {
		if read > 0 {
			t.fingerprint, _ = fileFingerprint(t.file, t.offset)
		}
	}()
	lines := t.splitLines(limit)
	if limit > 0 && len(lines) >= limit {
		return lines, read, nil
	}
	buf := make([]byte, logTailReadChunk)
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			read += int64(n)
			t.offset += int64(n)
			t.dirty = true
			t.partial = append(t.partial, buf[:n]...)
			remaining := 0
			if limit > 0 {
				remaining = limit - len(lines)
			}
			lines = append(lines, t.splitLines(remaining)...)
			if limit > 0 && len(lines) >= limit {
				return lines, read, nil
			}
		}
		if err == io.EOF {
			return lines, read, nil
		}
		if err != nil {
			return lines, read, err
		}
		if n == 0 {
			return lines, read, nil
		}
	}
}

// splitLines takes up to limit complete lines from the buffer. An oversized
// line is emitted at the byte cap and its remainder is discarded through the
// newline, keeping an unbounded writer from growing the agent's memory.
func (t *tailReader) splitLines(limit int) []string {
	var lines []string
	for {
		if limit > 0 && len(lines) >= limit {
			break
		}
		if t.discarding {
			i := bytes.IndexByte(t.partial, '\n')
			if i < 0 {
				t.partial = nil
				break
			}
			t.partial = t.partial[i+1:]
			t.discarding = false
			continue
		}
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			if t.maxLine > 0 && len(t.partial) > t.maxLine {
				lines = append(lines, agentnode.CapText(string(t.partial), t.maxLine))
				t.partial = nil
				t.discarding = true
			}
			break
		}
		line := t.partial[:i]
		if t.maxLine > 0 && len(line) > t.maxLine {
			line = []byte(agentnode.CapText(string(line), t.maxLine))
		}
		lines = append(lines, strings.TrimSuffix(string(line), "\r"))
		t.partial = t.partial[i+1:]
	}
	return lines
}

// close releases the open file.
func (t *tailReader) close() {
	if t.file != nil {
		t.file.Close()
		t.file = nil
	}
}
