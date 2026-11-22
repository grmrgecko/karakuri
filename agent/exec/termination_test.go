//go:build !windows

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessTermination exercises real POSIX signal handlers so cancellation
// allows cleanup but never becomes an accepted zero exit.
func TestProcessTermination(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		for _, tc := range []struct {
			name, grace, behavior string
			timeout               bool
			cleaned               bool
		}{
			{name: "cooperative cancellation", grace: "500ms", behavior: "trap 'sleep 0.1; printf cleaned > \"$MARKER\"; exit 0' TERM", cleaned: true},
			{name: "cooperative timeout", grace: "500ms", behavior: "trap 'sleep 0.1; printf cleaned > \"$MARKER\"; exit 0' TERM", timeout: true, cleaned: true},
			{name: "forced escalation", grace: "100ms", behavior: "trap '' TERM"},
			{name: "immediate default", behavior: "trap 'sleep 0.1; printf cleaned > \"$MARKER\"; exit 0' TERM"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				ready, marker := filepath.Join(dir, "ready"), filepath.Join(dir, "cleaned")
				source := tc.behavior + "\nprintf ready > \"$READY\"\nwhile :; do sleep 0.02; done\n"
				cfg := map[string]any{"command": "sh", "args": []string{"-c", source}, "script": "#!/bin/sh\n" + source, "env": map[string]string{"READY": ready, "MARKER": marker}, "accepted_exit_codes": []int{0}, "termination_grace": tc.grace}
				if tc.timeout {
					cfg["timeout"] = 1
				}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				var n nodeapi.NodeType = &commandNode{}
				if kind == "script" {
					n = &scriptNode{}
				}
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				done := make(chan error, 1)
				go func() {
					_, err := n.Execute(ctx, processTestContext{config: string(raw)}, nil)
					done <- err
				}()
				require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 3*time.Second, 10*time.Millisecond, "the fixture must install its signal handler before cancellation")
				started := time.Now()
				if !tc.timeout {
					cancel()
				}
				select {
				case err = <-done:
				case <-time.After(4 * time.Second):
					t.Fatal("process cleanup exceeded its bounded grace")
				}
				if tc.timeout {
					require.Error(t, err, "a cleaned-up timeout must still fail")
				} else {
					assert.ErrorIs(t, err, context.Canceled, "accepted zero exit must not hide cancellation")
				}
				data, err := os.ReadFile(marker)
				if tc.cleaned {
					require.NoError(t, err, "the TERM handler must write its cleanup marker")
					assert.Equal(t, "cleaned", string(data))
				} else {
					assert.ErrorIs(t, err, os.ErrNotExist, "forced termination must not run the cleanup handler")
				}
				if tc.name == "forced escalation" {
					assert.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond, "an ignoring process receives its full grace")
				}
			})
		}
	}
}

// TestProcessTerminationAfterLeaderExit catches descendants that outlive their
// leader, including streams that bypass os/exec's output-copy pipes.
func TestProcessTerminationAfterLeaderExit(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		for _, mode := range []string{"return", "file", "discard"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				childPath := filepath.Join(dir, "child.sh")
				ready, heartbeat := filepath.Join(dir, "ready"), filepath.Join(dir, "heartbeat")
				require.NoError(t, os.WriteFile(childPath, []byte("#!/bin/sh\ntrap '' TERM\nprintf ready > \"$READY\"\nwhile :; do printf x >> \"$HEARTBEAT\"; sleep 0.02; done\n"), 0700))
				source := "trap 'exit 0' TERM\nsh \"$CHILD\" &\nwait\n"
				cfg := map[string]any{"command": "sh", "args": []string{"-c", source}, "script": "#!/bin/sh\n" + source, "env": map[string]string{"CHILD": childPath, "READY": ready, "HEARTBEAT": heartbeat}, "termination_grace": "150ms", "stdout_mode": mode, "stderr_mode": mode}
				if mode == "file" {
					cfg["stdout_path"], cfg["stderr_path"] = filepath.Join(dir, "out"), filepath.Join(dir, "err")
				}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				var n nodeapi.NodeType = &commandNode{}
				if kind == "script" {
					n = &scriptNode{}
				}
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				done := make(chan error, 1)
				go func() { _, err := n.Execute(ctx, processTestContext{config: string(raw)}, nil); done <- err }()
				require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 3*time.Second, 10*time.Millisecond)
				cancel()
				select {
				case err = <-done:
				case <-time.After(4 * time.Second):
					t.Fatal("the canceled process group did not finish cleanup")
				}
				assert.ErrorIs(t, err, context.Canceled)
				before, err := os.ReadFile(heartbeat)
				require.NoError(t, err)
				time.Sleep(100 * time.Millisecond)
				after, err := os.ReadFile(heartbeat)
				require.NoError(t, err)
				assert.Equal(t, before, after, "the descendant must stop writing even after the leader exits")
			})
		}
	}
}

// TestProcessTerminationReachesEscapedDescendants covers a child that calls
// setsid, which takes it out of the launched process group so the group
// signal alone used to leave it running after cancellation. util-linux setsid
// runs in place when its caller is not a group leader, keeping the parent
// link the cleanup walk follows.
func TestProcessTerminationReachesEscapedDescendants(t *testing.T) {
	if _, err := osexec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not installed")
	}
	dir := t.TempDir()
	childPath := filepath.Join(dir, "child.sh")
	ready, heartbeat := filepath.Join(dir, "ready"), filepath.Join(dir, "heartbeat")
	require.NoError(t, os.WriteFile(childPath, []byte("#!/bin/sh\ntrap '' TERM\nprintf ready > \"$READY\"\nwhile :; do printf x >> \"$HEARTBEAT\"; sleep 0.02; done\n"), 0700))
	for _, grace := range []string{"0s", "150ms"} {
		t.Run(grace, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(ready))
			source := "setsid sh \"$CHILD\" &\nwait\n"
			raw, err := json.Marshal(map[string]any{"command": "sh", "args": []string{"-c", source}, "env": map[string]string{"CHILD": childPath, "READY": ready, "HEARTBEAT": heartbeat}, "termination_grace": grace})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				_, err := (&commandNode{}).Execute(ctx, processTestContext{config: string(raw)}, nil)
				done <- err
			}()
			require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 3*time.Second, 10*time.Millisecond)
			cancel()
			select {
			case err = <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("cancellation did not finish")
			}
			assert.ErrorIs(t, err, context.Canceled)
			before, err := os.ReadFile(heartbeat)
			require.NoError(t, err)
			time.Sleep(100 * time.Millisecond)
			after, err := os.ReadFile(heartbeat)
			require.NoError(t, err)
			assert.Equal(t, before, after, "a descendant in its own session must stop with the command")
		})
	}
}

// TestTerminationGraceConfiguration checks serialized duration boundaries and
// verifies rejected settings do not launch or overwrite the configured output.
func TestTerminationGraceConfiguration(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		for _, tc := range []struct {
			grace string
			valid bool
		}{
			{"", true}, {"0s", true}, {"0.5s", true}, {"1m", true},
			{"-1s", false}, {"1m1ns", false}, {"bad", false},
		} {
			t.Run(fmt.Sprintf("%s/%s", kind, tc.grace), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "out")
				require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
				cfg := map[string]any{"command": "sh", "args": []string{"-c", "printf completed"}, "script": "#!/bin/sh\nprintf completed", "termination_grace": tc.grace, "stdout_path": path}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				var n nodeapi.NodeType = &commandNode{}
				if kind == "script" {
					n = &scriptNode{}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				out, err := n.Execute(ctx, processTestContext{config: string(raw)}, nil)
				if tc.valid {
					require.NoError(t, err, "normal completion must not wait for the configured grace")
					assert.Equal(t, "completed", out["out"][0].Payload)
				} else {
					assert.ErrorContains(t, err, "termination grace")
				}
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				want := "original"
				if tc.valid {
					want = "completed"
				}
				assert.Equal(t, want, string(data), "invalid grace must preserve the output destination")
			})
		}
	}
}

// TestBackgroundDescendantKeepsSuccessfulExit verifies a program that exits
// zero while a background descendant still holds its mirrored output succeeds
// with what it wrote. It pins the regression where the mirror's copy pipe
// outlived the program, the wait delay expired, and the node failed a run that
// had exited successfully, although the same program without a mirror passed.
func TestBackgroundDescendantKeepsSuccessfulExit(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			source := "sleep 7 &\necho started\n"
			raw, err := json.Marshal(map[string]any{
				"command": "sh", "args": []string{"-c", source}, "script": "#!/bin/sh\n" + source,
				"output_format": "text", "stdout_path": filepath.Join(t.TempDir(), "stdout.log"),
			})
			require.NoError(t, err)
			var n nodeapi.NodeType = &commandNode{}
			if kind == "script" {
				n = &scriptNode{}
			}
			out, err := n.Execute(context.Background(), processTestContext{config: string(raw)}, nil)
			require.NoError(t, err, "a zero exit must not fail because a descendant kept the output pipe open")
			require.Len(t, out["out"], 1, "the successful run must emit one message")
			assert.Equal(t, "started", out["out"][0].Payload, "output written before the exit must be returned")
			assert.Equal(t, 0, out["out"][0].Meta["exit_code"], "the reported exit code must be the program's own")
		})
	}
}

// TestTimeoutDuringWaitDelayKeepsSuccessfulExit verifies a program that exits
// zero before its timeout succeeds even when the deadline passes while a
// background descendant holds the output pipe. os/exec stops watching the
// context once the program exits, so no cancellation ran: the 2s deadline
// falls inside the 5s wait delay, and the run must not be reported as a
// timeout for a process nothing stopped.
func TestTimeoutDuringWaitDelayKeepsSuccessfulExit(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			source := "sleep 7 &\necho started\n"
			raw, err := json.Marshal(map[string]any{
				"command": "sh", "args": []string{"-c", source}, "script": "#!/bin/sh\n" + source,
				"output_format": "text", "stdout_path": filepath.Join(t.TempDir(), "stdout.log"),
				"timeout": 2,
			})
			require.NoError(t, err)
			var n nodeapi.NodeType = &commandNode{}
			if kind == "script" {
				n = &scriptNode{}
			}
			out, err := n.Execute(context.Background(), processTestContext{config: string(raw)}, nil)
			require.NoError(t, err, "a zero exit before the deadline must not be reported as a timeout")
			require.Len(t, out["out"], 1, "the successful run must emit one message")
			assert.Equal(t, "started", out["out"][0].Payload, "output written before the exit must be returned")
			assert.Equal(t, 0, out["out"][0].Meta["exit_code"], "the reported exit code must be the program's own")
		})
	}
}

// TestOutputPathOnCharacterDevice verifies a stream mirrored to /dev/null
// keeps the run's result. fsync on a character device, FIFO, or socket returns
// EINVAL on Linux and the BSDs, which failed the node after the program had
// already finished.
func TestOutputPathOnCharacterDevice(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"command": "sh", "args": []string{"-c", "echo hi"},
		"output_format": "text", "stderr_path": "/dev/null",
	})
	require.NoError(t, err)
	out, err := (&commandNode{}).Execute(context.Background(), processTestContext{config: string(raw)}, nil)
	require.NoError(t, err, "a device as the mirror must not fail a finished run")
	require.Len(t, out["out"], 1, "the successful run must emit one message")
	assert.Equal(t, "hi", out["out"][0].Payload, "standard output must still be returned")
}
