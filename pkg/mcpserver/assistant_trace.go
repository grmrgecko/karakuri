package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const assistantTraceLimit = 32 << 20

// assistantTrace records one opted-in turn without HTTP authentication headers.
type assistantTrace struct {
	file      *os.File
	bytes     int
	truncated bool
}

func newAssistantTrace(dir string) (*assistantTrace, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating assistant trace directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "turn-"+time.Now().UTC().Format("20060102T150405Z")+"-*.ndjson")
	if err != nil {
		return nil, fmt.Errorf("creating assistant trace: %w", err)
	}
	return &assistantTrace{file: file}, nil
}

func (t *assistantTrace) write(kind string, data any) error {
	if t == nil || t.truncated {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"time": time.Now().UTC(), "type": kind, "data": data})
	if err != nil {
		return fmt.Errorf("encoding assistant trace: %w", err)
	}
	// Leave room for the truncation marker so the complete file stays bounded.
	if t.bytes+len(raw)+1 > assistantTraceLimit-1024 {
		t.truncated = true
		raw = []byte(`{"type":"trace.truncated","data":{"reason":"32 MiB turn trace limit reached"}}`)
	}
	n, err := t.file.Write(append(raw, '\n'))
	t.bytes += n
	if err != nil {
		return fmt.Errorf("writing assistant trace: %w", err)
	}
	return nil
}

func (t *assistantTrace) close(turnErr error) error {
	if t == nil {
		return nil
	}
	status := map[string]any{"status": "finished"}
	if turnErr != nil {
		status = map[string]any{"status": "error", "error": turnErr.Error()}
	}
	err := t.write("turn.end", status)
	closeErr := t.file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("closing assistant trace: %w", closeErr)
	}
	return nil
}
