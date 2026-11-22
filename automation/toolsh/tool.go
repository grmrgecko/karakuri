// Package toolsh drives the desktop through the programs a session already has:
// the automation backends resolve, run, and launch tools rather than linking a
// library for each, so what works depends on what is installed rather than on
// which OS this is. The helpers are stdlib-only so any caller can share them.
package toolsh

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// ToolPath returns the first of the named programs found in PATH. A backend
// drives the desktop through the tools the session already has, so the answer
// depends on what is installed rather than on which OS this is.
func ToolPath(names ...string) (string, bool) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// HasTool reports whether any of the named programs is installed, for the
// callers that pick a code path rather than a program.
func HasTool(names ...string) bool {
	_, ok := ToolPath(names...)
	return ok
}

// RunTool runs a program and returns its stdout. A non-zero exit becomes an
// error carrying the program's own stderr, which is the only place a desktop
// tool explains itself.
func RunTool(ctx context.Context, name string, args ...string) (string, error) {
	return RunToolInput(ctx, "", name, args...)
}

// RunToolInput is RunTool with stdin, for the tools that take their argument
// that way (wl-copy, xclip, clip.exe).
func RunToolInput(ctx context.Context, stdin, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(errBuf.String()); detail != "" {
			return out.String(), fmt.Errorf("%s: %s", name, FirstLine(detail))
		}
		return out.String(), fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
}

// StartDetached launches a program and does not wait for it. Opening an
// application means leaving it running after the call returns, so the process
// gets no context and is reaped in the background rather than left a zombie.
func StartDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// CapText truncates text to a byte budget without splitting a character in
// half, which is what a plain slice does to anything outside ASCII.
func CapText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// FirstLine trims a tool's complaint to its first line, which is the part worth
// putting in an error.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
