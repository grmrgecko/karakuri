package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandWords pins command-file quoting without treating Windows path
// separators or shell metacharacters as escapes or executable syntax.
func TestCommandWords(t *testing.T) {
	for _, tc := range []struct {
		line    string
		words   []string
		invalid bool
	}{
		{line: `run "C:\Program Files\App\app.exe" "" --wait`, words: []string{"run", `C:\Program Files\App\app.exe`, "", "--wait"}},
		{line: `type "say ""hello"""`, words: []string{"type", `say "hello"`}},
		{line: `type 'C:\path\file'`, words: []string{"type", `C:\path\file`}},
		{line: `type a&b > file`, words: []string{"type", "a&b", ">", "file"}},
		{line: `run "unfinished`, invalid: true},
	} {
		got, err := commandWords(tc.line)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tc.words, got)
	}
}

// TestScriptLineCommand pins the command-file guard: a line cannot start a
// nested script or manage the resident. It covers the regression where the
// --local flag in front of the command hid it from the guard, so a command
// file could run itself without end.
func TestScriptLineCommand(t *testing.T) {
	for _, tc := range []struct {
		line    string
		args    []string
		command string
		refused bool
	}{
		{line: `type hello`, args: []string{"type", "hello"}, command: "type"},
		{line: `karakuri-auto.exe press ctrl+s`, args: []string{"press", "ctrl+s"}, command: "press"},
		{line: `--local type hello`, args: []string{"--local", "type", "hello"}, command: "type"},
		{line: `script other.txt`, refused: true},
		{line: `karakuri-auto resident serve`, refused: true},
		{line: `--local script other.txt`, refused: true},
		{line: `karakuri-auto --local resident serve`, refused: true},
		{line: `karakuri-auto`, refused: true},
		{line: `--local`, refused: true},
	} {
		words, err := commandWords(tc.line)
		require.NoError(t, err)
		args, command, err := scriptLineCommand(words)
		if tc.refused {
			assert.Error(t, err, "the line %q must not run from a command file", tc.line)
			continue
		}
		require.NoError(t, err, "the line %q is an ordinary command", tc.line)
		assert.Equal(t, tc.args, args, "the line %q must keep its arguments", tc.line)
		assert.Equal(t, tc.command, command, "the line %q must be logged by its command", tc.line)
	}
}
