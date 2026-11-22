package system

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignalMissingPID verifies "Fail when nothing matched" decides the
// outcome for a pid target the same way it does for a pattern. The pid
// belongs to a child that was started and reaped, so no process holds it.
func TestSignalMissingPID(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^$")
	require.NoError(t, child.Run(), "the short-lived child must run to completion")
	pid := child.Process.Pid

	for _, tc := range []struct {
		name         string
		requireMatch any
		wantErr      bool
	}{
		{name: "default fails", requireMatch: nil, wantErr: true},
		{name: "required fails", requireMatch: true, wantErr: true},
		{name: "not required passes through", requireMatch: false},
	} {
		cfg := map[string]any{"target": "pid", "pid": pid, "signal": "terminate"}
		if tc.requireMatch != nil {
			cfg["require_match"] = tc.requireMatch
		}
		out, err := (&processSignalNode{}).Execute(context.Background(), httpContext(t, cfg),
			map[string]*nodeapi.Msg{"in": {Payload: "keep"}})
		if tc.wantErr {
			assert.ErrorContains(t, err, "no process matched", "%s: a missing pid must fail when a match is required", tc.name)
			continue
		}
		require.NoError(t, err, "%s: a missing pid must pass through when no match is required", tc.name)
		require.Len(t, out["out"], 1, "%s: the pass-through must emit one message", tc.name)
		assert.Equal(t, "keep", out["out"][0].Payload, "%s: the input payload must pass through", tc.name)
		assert.Equal(t, 0, out["out"][0].Meta["count"], "%s: nothing was signalled", tc.name)
		assert.Equal(t, []any{}, out["out"][0].Meta["signalled"], "%s: the signalled list must be empty", tc.name)
	}
}
