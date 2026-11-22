package exec

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// TestParseShebang pins how a first line becomes an interpreter. The key is
// what the interpreter map and the PATH lookup both use, so a form that parsed
// to the wrong word would send a script to the wrong program or to none.
func TestParseShebang(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		want     string
		args     []string
		absolute bool
		ok       bool
	}{
		{name: "env python", source: "#!/usr/bin/env python3\nprint(1)", want: "python3", ok: true},
		{name: "absolute bash", source: "#!/bin/bash\necho hi", want: "bash", absolute: true, ok: true},
		{name: "absolute sh", source: "#!/bin/sh", want: "sh", absolute: true, ok: true},
		{name: "env node", source: "#!/usr/bin/env node", want: "node", ok: true},
		{name: "crlf line ending", source: "#!/bin/bash\r\necho hi", want: "bash", absolute: true, ok: true},
		// A bare name is not a path any kernel can exec, which is exactly the
		// case PATH lookup exists for and the one Windows always takes.
		{name: "bare name", source: "#!python3\nprint(1)", want: "python3", ok: true},
		{name: "powershell", source: "#!powershell\nWrite-Host hi", want: "powershell", ok: true},
		{name: "cmd", source: "#!cmd\n@echo hi", want: "cmd", ok: true},
		// Arguments ride along so the interpreter is invoked the way the
		// shebang asked, whether the kernel reads it or the agent does.
		{name: "interpreter arguments", source: "#!/bin/bash -e\n", want: "bash", args: []string{"-e"}, absolute: true, ok: true},
		{name: "env split-string", source: "#!/usr/bin/env -S python3 -u\n", want: "python3", args: []string{"-u"}, ok: true},
		{name: "no shebang", source: "echo hi"},
		{name: "blank", source: ""},
		{name: "hash but not shebang", source: "# a comment"},
		{name: "env with nothing after it", source: "#!/usr/bin/env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb, ok := parseShebang(tc.source)
			assert.Equal(t, tc.ok, ok, "whether a first line is a shebang at all")
			assert.Equal(t, tc.want, sb.Key, "the key is what PATH and the interpreter map are asked for")
			assert.Equal(t, tc.args, sb.Args, "arguments the shebang carried")
			assert.Equal(t, tc.absolute, sb.Absolute,
				"only a path can be exec'd directly; a bare name has to be looked up")
		})
	}
}

// TestScriptSourceNeutralizesShebangForCmd covers the one interpreter that
// cannot read its own shebang. Every other one here treats # as a comment, but
// cmd would try to run the line and print "not recognized" to stderr on every
// single execution, which a flow watching stderr would act on. The line is
// rewritten rather than dropped so an error still names the line the author
// wrote.
func TestScriptSourceNeutralizesShebangForCmd(t *testing.T) {
	const batch = "#!cmd\n@echo off\necho hi\n"
	sb, ok := parseShebang(batch)
	require.True(t, ok, "the fixture must parse as a shebang")

	got := scriptSource(batch, sb, ok)
	assert.Equal(t, "@rem #!cmd\n@echo off\necho hi\n", got,
		"cmd's shebang becomes a batch comment in place")
	assert.Equal(t, strings.Count(batch, "\n"), strings.Count(got, "\n"),
		"the rewrite must not move any line, or an error's line number stops matching the source")

	const py = "#!/usr/bin/env python3\nprint(1)\n"
	pysb, ok := parseShebang(py)
	require.True(t, ok, "the fixture must parse as a shebang")
	assert.Equal(t, py, scriptSource(py, pysb, ok),
		"an interpreter that reads # as a comment gets its source untouched")
}

// TestScriptExtNamesTheFile covers the extensions Windows actually keys on:
// cmd refuses a file that is not .bat or .cmd, and PowerShell's -File wants
// .ps1. An interpreter with nothing to declare gets no extension rather than a
// guess.
func TestScriptExtNamesTheFile(t *testing.T) {
	for source, want := range map[string]string{
		"#!cmd\n":                  ".bat",
		"#!powershell\n":           ".ps1",
		"#!pwsh\n":                 ".ps1",
		"#!/usr/bin/env python3\n": ".py",
		"#!/bin/bash\n":            ".sh",
		"#!/usr/bin/env deno\n":    "",
	} {
		sb, ok := parseShebang(source)
		require.Truef(t, ok, "%q must parse as a shebang", source)
		assert.Equalf(t, want, scriptExt(sb, ok), "extension for %q", source)
	}
	sb, ok := parseShebang("echo hi")
	assert.Empty(t, scriptExt(sb, ok), "a script with no shebang gets no extension")
}

func TestDecodeOutput(t *testing.T) {
	t.Run("auto parses json", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("auto", []byte(`{"a":1}`))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": float64(1)}, v)
	})
	t.Run("auto falls back to text", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("auto", []byte("hello\n"))
		require.NoError(t, err)
		assert.Equal(t, "hello", v)
	})
	t.Run("json strict rejects text", func(t *testing.T) {
		_, err := agentnode.DecodeOutput("json", []byte("not json"))
		assert.Error(t, err)
	})
	t.Run("text trims trailing newline", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("text", []byte("line\n"))
		require.NoError(t, err)
		assert.Equal(t, "line", v)
	})
	t.Run("text trims a Windows CRLF", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("text", []byte("DESKTOP-1\r\n"))
		require.NoError(t, err)
		assert.Equal(t, "DESKTOP-1", v)
	})
	t.Run("auto trims a Windows CRLF", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("auto", []byte("DESKTOP-1\r\n"))
		require.NoError(t, err)
		assert.Equal(t, "DESKTOP-1", v)
	})
	t.Run("binary is base64", func(t *testing.T) {
		v, err := agentnode.DecodeOutput("binary", []byte{0x00, 0x01, 0x02})
		require.NoError(t, err)
		assert.Equal(t, "AAEC", v)
	})
	t.Run("unknown format errors", func(t *testing.T) {
		_, err := agentnode.DecodeOutput("xml", []byte("x"))
		assert.Error(t, err)
	})
}

// TestScriptRunsWithCRLFShebang locks that a shebang line ending in CRLF
// starts its interpreter when the kernel reads the line. parseShebang dropped
// the carriage return, but the file was written with it, so the kernel looked
// for an interpreter named "/bin/sh\r" and the script failed to start with
// "no such file or directory".
func TestScriptRunsWithCRLFShebang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture needs a kernel that reads the shebang line")
	}
	for _, tc := range []struct {
		name   string
		script string
	}{
		{name: "LF", script: "#!/bin/sh\nprintf ok\n"},
		{name: "CRLF", script: "#!/bin/sh\r\nprintf ok\n"},
		{name: "CRLF with an argument", script: "#!/bin/sh -e\r\nprintf ok\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"script": tc.script, "output_format": "text"})
			require.NoError(t, err)
			out, err := (&scriptNode{}).Execute(context.Background(), processTestContext{config: string(raw)},
				map[string]*nodeapi.Msg{"in": {}})
			require.NoError(t, err, "The line ending of the shebang must not change which interpreter starts.")
			assert.Equal(t, "ok", out["out"][0].Payload, "The script runs under the interpreter the shebang names.")
		})
	}

	const source = "#!/bin/sh\r\necho one\r\necho two\r\n"
	sb, ok := parseShebang(source)
	require.True(t, ok, "The fixture must parse as a shebang.")
	assert.Equal(t, "#!/bin/sh\necho one\r\necho two\r\n", scriptSource(source, sb, ok),
		"Only the shebang line loses its carriage return, so every line keeps its number.")
}

// TestConcurrentScriptsStart locks that scripts started from several
// goroutines all run, as concurrent flow runs start them. A child forked while
// another run still had its script open for writing inherited that descriptor,
// and Linux refuses to execute a file open for writing, so about 1 run in 70
// failed with "text file busy". The race needs real forks to show, so the test
// is statistical: 8 workers by 40 runs caught the defect in nearly every run
// before the write was kept clear of forks, and cannot fail once it is.
func TestConcurrentScriptsStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture needs a kernel that reads the shebang line")
	}
	raw, err := json.Marshal(map[string]any{"script": "#!/bin/sh\nexit 0\n"})
	require.NoError(t, err, "the fixture configuration must encode")

	const workers, rounds = 8, 40
	failures := make(chan error, workers*rounds)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				_, err := (&scriptNode{}).Execute(context.Background(), processTestContext{config: string(raw)},
					map[string]*nodeapi.Msg{"in": {}})
				if err != nil {
					failures <- err
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		assert.NoError(t, err, "a script must start while other runs are writing and starting theirs")
	}
}
