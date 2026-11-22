package exec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandResolvesInterpreter covers the two ways a shebang answers the
// question, on a real host. A path is authoritative and nothing is looked up; a
// bare name goes to PATH exactly as a shell would resolve it. There is no third
// way and no interpreter map: an install that wants a particular program either
// names it in full or puts it on the PATH of the user the agent runs as.
func TestCommandResolvesInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the direct-exec case is unix-only")
	}
	ctx := context.Background()

	// The absolute branch chmods the file, so it needs a real one.
	path := filepath.Join(t.TempDir(), "script")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o600))

	sb, ok := parseShebang("#!/bin/sh\necho hi")
	require.True(t, ok)
	cmd, err := command(ctx, sb, ok, path)
	require.NoError(t, err)
	assert.Equal(t, path, cmd.Path,
		"a path the kernel can read is left to the kernel, so the whole shebang line applies")

	sb, ok = parseShebang("#!python3\nprint(1)")
	require.True(t, ok)
	cmd, err = command(ctx, sb, ok, path)
	require.NoError(t, err, "a bare name must resolve on PATH with nothing configured")
	assert.Equal(t, "/usr/bin/python3", cmd.Path)
	assert.Equal(t, []string{"/usr/bin/python3", path}, cmd.Args)

	sb, ok = parseShebang("#!/usr/bin/env nosuchinterp\n")
	require.True(t, ok)
	_, err = command(ctx, sb, ok, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nosuchinterp", "the error names what was looked for")
	assert.Contains(t, err.Error(), "PATH", "and says where it was looked for")
}

// TestCommandAbsoluteShebangWinsWithoutKernelSupport pins the case that used to
// need a configured interpreter map. Windows cannot read a shebang, so the line
// has to be applied by the agent — and applying it means running the program
// the author named, not looking its basename up on PATH and finding a different
// install or nothing at all. The shebang is built rather than parsed because
// filepath.IsAbs only recognizes a Windows path when running on Windows.
func TestCommandAbsoluteShebangWinsWithoutKernelSupport(t *testing.T) {
	sb := shebang{Key: "python.exe", Path: `C:\Python311\python.exe`, Absolute: true}

	cmd, err := commandOn(context.Background(), false, sb, true, "s.py")
	require.NoError(t, err, "an absolute shebang must not be looked up at all")
	assert.Equal(t, `C:\Python311\python.exe`, cmd.Path,
		"the interpreter is the one the shebang named, not whatever PATH holds")
	assert.Equal(t, []string{`C:\Python311\python.exe`, "s.py"}, cmd.Args)

	// The same line where the kernel does read it: the file runs itself.
	unix := shebang{Key: "python3", Path: "/opt/python/bin/python3", Absolute: true}
	path := filepath.Join(t.TempDir(), "script")
	require.NoError(t, os.WriteFile(path, []byte("#!/opt/python/bin/python3\n"), 0o600))
	cmd, err = commandOn(context.Background(), true, unix, true, path)
	require.NoError(t, err)
	assert.Equal(t, path, cmd.Path, "the kernel applies the line, so the script is what runs")
}

// TestCommandWindowsInterpreterArgs checks that an interpreter needing its own
// arguments still gets them when the agent is the one applying the shebang,
// which is every script on Windows.
func TestCommandWindowsInterpreterArgs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sb     shebang
		script string
		want   []string
	}{
		{
			name:   "powershell by path",
			sb:     shebang{Key: "powershell", Path: `C:\ps.exe`, Absolute: true},
			script: "s.ps1",
			want:   []string{`C:\ps.exe`, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "s.ps1"},
		},
		{
			name:   "cmd by path",
			sb:     shebang{Key: "cmd", Path: `C:\cmd.exe`, Absolute: true},
			script: "s.bat",
			want:   []string{`C:\cmd.exe`, "/c", "s.bat"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := commandOn(context.Background(), false, tc.sb, true, tc.script)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cmd.Args)
		})
	}
}

// TestInterpreterArgs checks the table directly, since the interpreters it
// describes are not installed on the host the tests usually run on.
func TestInterpreterArgs(t *testing.T) {
	sb, ok := parseShebang("#!powershell\nWrite-Host hi")
	require.True(t, ok)
	assert.Equal(t,
		[]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "s.ps1"},
		interpreterArgs(interpreters[sb.Key], sb, "s.ps1"),
		"PowerShell needs -File, and refuses an unsigned script without the policy override")

	sb, ok = parseShebang("#!cmd\n@echo hi")
	require.True(t, ok)
	assert.Equal(t, []string{"/c", "s.bat"}, interpreterArgs(interpreters[sb.Key], sb, "s.bat"))

	sb, ok = parseShebang("#!/bin/bash -e\n")
	require.True(t, ok)
	assert.Equal(t, []string{"-e", "s.sh"}, interpreterArgs(interpreters[sb.Key], sb, "s.sh"),
		"arguments the shebang carried reach the interpreter")
}

// TestScriptRunsViaPath is the resolution running for real with nothing
// configured, which is the case that used to be impossible on Windows.
func TestScriptRunsViaPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	src := "#!sh\necho hello from path\n"
	sb, ok := parseShebang(src)
	require.True(t, ok)
	cmd, err := command(context.Background(), sb, ok, "/dev/stdin")
	require.NoError(t, err)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "hello from path\n", string(out))
}

// TestInterpreterNamedWithExeSuffix locks that a shebang naming a Windows
// interpreter by its file name gets the same handling as the bare name. On
// Windows the base name of C:\Windows\System32\cmd.exe is cmd.exe, which the
// table did not know: cmd.exe started without /c, read the message JSON on
// stdin as commands, and PowerShell was handed a file with no .ps1 extension
// and no -File. Windows file names are case-insensitive, so the spelling
// Windows itself uses, PowerShell.exe, is covered too.
func TestInterpreterNamedWithExeSuffix(t *testing.T) {
	for _, tc := range []struct {
		shebang  string
		wantExt  string
		wantArgs []string
	}{
		{"#!cmd.exe", ".bat", []string{"/c", "script.bat"}},
		{"#!PowerShell.exe", ".ps1", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "script.ps1"}},
		{"#!/usr/bin/env pwsh.exe", ".ps1", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "script.ps1"}},
	} {
		source := tc.shebang + "\necho hi\n"
		sb, ok := parseShebang(source)
		require.Truef(t, ok, "%s is a shebang", tc.shebang)
		ext := scriptExt(sb, ok)
		assert.Equalf(t, tc.wantExt, ext, "extension mismatch for %s", tc.shebang)

		// The interpreter is built from its path, as on Windows, so the name
		// need not be installed on the host running the test.
		sb.Path, sb.Absolute = sb.Key, true
		cmd, err := commandOn(context.Background(), false, sb, true, "script"+ext)
		require.NoErrorf(t, err, "%s must build a command", tc.shebang)
		assert.Equalf(t, tc.wantArgs, cmd.Args[1:], "argument mismatch for %s", tc.shebang)
	}

	assert.True(t, strings.HasPrefix(scriptSource("#!cmd.exe\n@echo hi\n", shebang{Key: "cmd.exe"}, true), "@rem #!cmd.exe"),
		"cmd.exe cannot read the shebang as a comment any more than cmd can")
}
