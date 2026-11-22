package exec

import (
	"fmt"
	"io"
	"os"
)

// maxProcessOutput bounds what one captured stream hands back. The spool on
// disk is unbounded, but the result crosses the wire and sits in memory, so
// past this much the node fails and points at the file mirror, which has no
// limit.
const maxProcessOutput = 32 << 20

// outputCapture drains a process stream to disk while it runs. The temporary
// file avoids tying agent memory use to how much a child writes; mirror is the
// optional operator-named copy that remains after the run.
type outputCapture struct {
	temporary *os.File
	mirror    *os.File
	mode      string
}

// newOutputCapture opens only the destinations required by the stream policy.
func newOutputCapture(destination, mode string) (*outputCapture, error) {
	if mode == "" {
		mode = "return"
	}
	if err := validateCapture(mode, destination); err != nil {
		return nil, err
	}
	if mode == "discard" {
		return &outputCapture{mode: mode}, nil
	}
	if mode == "file" {
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", destination, err)
		}
		return &outputCapture{mirror: file, mode: mode}, nil
	}
	temporary, err := os.CreateTemp("", "karakuri-agent-output-*")
	if err != nil {
		return nil, fmt.Errorf("creating output spool: %w", err)
	}
	c := &outputCapture{temporary: temporary}
	if destination == "" {
		return c, nil
	}
	c.mirror, err = os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		c.cleanup()
		return nil, fmt.Errorf("opening %s: %w", destination, err)
	}
	return c, nil
}

// Writer returns the stream destination handed to exec.Cmd.
func (c *outputCapture) Writer() io.Writer {
	if c.mode == "discard" {
		return io.Discard
	}
	if c.mode == "file" {
		return c.mirror
	}
	if c.mirror != nil {
		return io.MultiWriter(c.temporary, c.mirror)
	}
	return c.temporary
}

// finish closes the named copy, rewinds the spool, and reads the complete
// stream. The caller owns the returned bytes; cleanup still removes the spool.
// Only a regular file is synced, since fsync on a device, FIFO, or socket
// returns EINVAL.
func (c *outputCapture) finish() ([]byte, error) {
	if c.mirror != nil {
		if info, err := c.mirror.Stat(); err == nil && info.Mode().IsRegular() {
			if err := c.mirror.Sync(); err != nil {
				return nil, err
			}
		}
		if err := c.mirror.Close(); err != nil {
			return nil, err
		}
		c.mirror = nil
	}
	if c.mode == "file" || c.mode == "discard" {
		return nil, nil
	}
	if _, err := c.temporary.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(c.temporary, maxProcessOutput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProcessOutput {
		return nil, fmt.Errorf("output exceeds %d MiB; select file-only mode and an output path",
			maxProcessOutput>>20)
	}
	return data, nil
}

func validateCapture(mode, path string) error {
	switch mode {
	case "", "return":
	case "file":
		if path == "" {
			return fmt.Errorf("file-only stream requires an output path")
		}
	case "discard":
		if path != "" {
			return fmt.Errorf("discarded stream cannot also write an output file")
		}
	default:
		return fmt.Errorf("unknown stream mode %q", mode)
	}
	return nil
}

// cleanup closes and removes files that belong only to this execution.
func (c *outputCapture) cleanup() {
	if c == nil {
		return
	}
	if c.mirror != nil {
		_ = c.mirror.Close()
	}
	if c.temporary != nil {
		name := c.temporary.Name()
		_ = c.temporary.Close()
		_ = os.Remove(name)
	}
}
