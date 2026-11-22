package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type processTestContext struct {
	nodeapi.ExecContext
	config string
}

// TestAcceptedExitsDoNotHideProcessFailures rejects cancellation and launch failures even when nonzero exits are allowed.
func TestAcceptedExitsDoNotHideProcessFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	for _, kind := range []string{"command", "script"} {
		for _, failure := range []string{"timeout", "missing interpreter", "invalid result parent"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				cfg := map[string]any{"command": "sh", "args": []string{"-c", "sleep 2; exit 1"}, "script": "#!/bin/sh\nsleep 2; exit 1", "accepted_exit_codes": []int{0, 1}, "ignore_exit_code": true}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				if failure == "missing interpreter" {
					missing := filepath.Join(t.TempDir(), "missing")
					cfg["command"], cfg["script"] = missing, "#!"+missing+"\nexit 1"
				}
				if failure == "invalid result parent" {
					cfg["result_destination"], cfg["result_property"] = "property", "payload.result"
				}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				var n nodeapi.NodeType = &commandNode{}
				if kind == "script" {
					n = &scriptNode{}
				}
				_, err = n.Execute(ctx, processTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": {Payload: "keep"}})
				require.Error(t, err)
				if failure == "timeout" {
					assert.ErrorIs(t, err, context.DeadlineExceeded)
				}
				if failure == "invalid result parent" {
					assert.ErrorContains(t, err, "parent must be an object")
				}
			})
		}
	}
}

func (c processTestContext) Config(v any) error                                 { return json.Unmarshal([]byte(c.config), v) }
func (processTestContext) Interpolate(s string, _ *nodeapi.Msg) (string, error) { return s, nil }
func (processTestContext) Debug(any, string)                                    {}
func (processTestContext) Vars() map[string]any                                 { return nil }

// TestProcessPolicies exercises real POSIX exits and verifies result placement does not mutate input.
func TestProcessPolicies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	for _, tc := range []struct {
		name     string
		code     int
		mode     string
		property string
		fail     bool
	}{
		{name: "accepted command exit", code: 1, mode: "command", property: "meta.job.result"},
		{name: "accepted script exit", code: 1, mode: "script", property: "payload.result"},
		{name: "rejected command exit", code: 2, mode: "command", fail: true},
		{name: "rejected script exit", code: 2, mode: "script", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"command": "sh", "args": []string{"-c", "printf '{\"ok\":true}'; exit " + string(rune('0'+tc.code))}, "script": "#!sh\nprintf '{\"ok\":true}'; exit " + string(rune('0'+tc.code)), "accepted_exit_codes": []int{0, 1}, "output_format": "json"}
			if tc.property != "" {
				cfg["result_destination"] = "property"
				cfg["result_property"] = tc.property
			}
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			ec := processTestContext{config: string(raw)}
			input := &nodeapi.Msg{Payload: map[string]any{"original": true}, Meta: map[string]any{"job": map[string]any{"id": 1}}}
			var n nodeapi.NodeType = &commandNode{}
			if tc.mode == "script" {
				n = &scriptNode{}
			}
			out, err := n.Execute(context.Background(), ec, map[string]*nodeapi.Msg{"in": input})
			if tc.fail {
				assert.ErrorContains(t, err, "exited with code 2")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 1, out["out"][0].Meta["exit_code"])
			assert.Equal(t, map[string]any{"original": true}, input.Payload, "the upstream payload must remain unchanged")
			assert.Equal(t, map[string]any{"job": map[string]any{"id": 1}}, input.Meta, "shared metadata descendants must remain unchanged")
			if tc.property == "meta.job.result" {
				assert.Equal(t, input.Payload, out["out"][0].Payload)
			}
		})
	}
}

// TestProcessFileOnlyAvoidsReturnedOutputLimit verifies a real program writes 32 MiB + 1 bytes without a returned spool.
func TestProcessFileOnlyAvoidsReturnedOutputLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses head and /dev/zero")
	}
	path := filepath.Join(t.TempDir(), "large.log")
	for _, mode := range []string{"file", "return", "discard"} {
		cfg := map[string]any{"command": "head", "args": []string{"-c", "33554433", "/dev/zero"}, "stdout_mode": mode, "output_format": "json"}
		if mode != "discard" {
			cfg["stdout_path"] = path
		}
		raw, err := json.Marshal(cfg)
		require.NoError(t, err)
		input := &nodeapi.Msg{Payload: "keep"}
		out, err := (&commandNode{}).Execute(context.Background(), processTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": input})
		if mode == "return" {
			assert.ErrorContains(t, err, "output exceeds 32 MiB")
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, "keep", out["out"][0].Payload)
		if mode == "file" {
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, int64(33554433), info.Size())
		}
	}
}

// TestProcessPortContract keeps config-derived types aligned with payload preservation and unknown auto output.
func TestProcessPortContract(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   string
		from   string
	}{
		{`{"output_format":"binary"}`, flow.PortBinary, "in"},
		{`{"output_format":"json","stdout_mode":"file"}`, flow.PortAny, "in"},
		{`{"output_format":"binary","result_destination":"property","result_property":"meta.result"}`, flow.PortAny, "in"},
		{`{"output_format":"binary","result_destination":"property","result_property":"payload.result"}`, flow.PortObject, "in"},
		{`{"output_format":"auto"}`, flow.PortAny, ""},
	} {
		ports := flow.NodePorts((&commandNode{}).Meta(), &flow.Node{Config: json.RawMessage(tc.config)}, nil)
		assert.Equal(t, tc.want, ports.Outputs[0].Type, "derived type for %s", tc.config)
		assert.Equal(t, tc.from, ports.Outputs[0].TypeFrom, "input propagation for %s", tc.config)
	}
}

// TestScriptLargePayloadMovesToFile pins the exec boundary for a payload too
// large for one environment string. Linux refuses execve with E2BIG when any
// single string passes MAX_ARG_STRLEN (32 pages, 128 KiB), so a 200 KiB payload
// must reach the script through stdin and PAYLOAD_FILE instead of PAYLOAD.
func TestScriptLargePayloadMovesToFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	script := "#!/bin/sh\nstdin=$(wc -c)\nfile=$(wc -c < \"$PAYLOAD_FILE\")\nprintf '%s %s %s' \"${PAYLOAD-unset}\" $stdin $file"
	raw, err := json.Marshal(map[string]any{"script": script, "output_format": "text"})
	require.NoError(t, err)
	input := &nodeapi.Msg{Payload: strings.Repeat("a", 200<<10)}

	out, err := (&scriptNode{}).Execute(context.Background(), processTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": input})
	require.NoError(t, err, "a large payload must not stop the script from starting")
	// 200 KiB of letters plus the two quotes of the JSON string, on stdin and in the file.
	want := fmt.Sprintf("unset %d %d", 200<<10+2, 200<<10+2)
	assert.Equal(t, want, out["out"][0].Payload, "the oversized value must leave PAYLOAD and arrive whole in its file")
}

// TestCommandArgumentsKeepSurroundingWhitespace verifies an argument reaches
// the program byte for byte. It pins the regression where each argument was
// trimmed, so a lone space given to cut -d or tr arrived as an empty string
// and an indented value lost its indentation.
func TestCommandArgumentsKeepSurroundingWhitespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses printf")
	}
	raw, err := json.Marshal(map[string]any{
		"command":       "printf",
		"args":          []string{"[%s][%s][%s]", " ", "  indented", "tab\t"},
		"output_format": "text",
	})
	require.NoError(t, err, "the fixture configuration must encode")

	out, err := (&commandNode{}).Execute(context.Background(), processTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": {}})
	require.NoError(t, err, "printf must run with the given arguments")
	require.Len(t, out["out"], 1, "the command emits one message")
	assert.Equal(t, "[ ][  indented][tab\t]", out["out"][0].Payload, "whitespace inside an argument is part of the argument")
}

// TestEnvironmentValuesKeepSurroundingWhitespace verifies an environment value
// reaches the program byte for byte from both process nodes. It pins the
// regression where each value was trimmed, so a separator set to a lone space
// arrived empty and an indented value lost its indentation.
func TestEnvironmentValuesKeepSurroundingWhitespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses sh and printf")
	}
	raw, err := json.Marshal(map[string]any{
		"command":       "sh",
		"args":          []string{"-c", `printf '[%s][%s]' "$SEP" "$INDENT"`},
		"script":        "#!/bin/sh\nprintf '[%s][%s]' \"$SEP\" \"$INDENT\"\n",
		"env":           map[string]string{"SEP": " ", "INDENT": "  two spaces"},
		"output_format": "text",
	})
	require.NoError(t, err, "the fixture configuration must encode")

	for _, tc := range []struct {
		name string
		node nodeapi.NodeType
	}{
		{"command", &commandNode{}},
		{"script", &scriptNode{}},
	} {
		out, err := tc.node.Execute(context.Background(), processTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": {}})
		require.NoErrorf(t, err, "the %s node must run with the given environment", tc.name)
		require.Lenf(t, out["out"], 1, "the %s node emits one message", tc.name)
		assert.Equalf(t, "[ ][  two spaces]", out["out"][0].Payload, "whitespace in a %s node's environment value is part of the value", tc.name)
	}
}
