package agentnode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type budgetTestContext struct {
	nodeapi.ExecContext
	config string
}

func (c budgetTestContext) Config(v any) error { return json.Unmarshal([]byte(c.config), v) }

type deadlineObserver struct {
	kind string
	// timeoutDefault is the default the node declares on its timeout field,
	// nil for a node that declares none.
	timeoutDefault any
}

func (n deadlineObserver) Meta() nodeapi.NodeMeta {
	meta := nodeapi.NodeMeta{Type: n.kind}
	if n.timeoutDefault != nil {
		meta.ConfigSpec = []nodeapi.FieldSpec{{ID: "timeout", Default: n.timeoutDefault}}
	}
	return meta
}
func (deadlineObserver) Execute(ctx context.Context, _ nodeapi.ExecContext, _ map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	deadline, _ := ctx.Deadline()
	return nodeapi.One("out", &nodeapi.Msg{Payload: time.Until(deadline)}), nil
}

// TestBudgetConfiguration verifies serialized timeout and cleanup allowances
// leave wrapped nodes time to finish after their own execution deadline.
func TestBudgetConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, config, kind string
		seconds            float64
	}{
		{"agent default", `{}`, "", 12},
		{"fractional HTTP seconds", `{"timeout":0.25}`, "", 10.25},
		{"probe duration", `{"timeout":"15m"}`, "", 910},
		{"service confirmation", `{"confirm":true,"confirmation_timeout":"7s"}`, "", 19},
		{"command termination", `{"termination_grace":"1m"}`, "command", 72},
		{"script termination", `{"termination_grace":"250ms"}`, "script", 12.25},
		{"unrelated termination field", `{"termination_grace":"1m"}`, "automation-script", 12},
		{"invalid termination duration", `{"termination_grace":"1m1ns"}`, "command", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := WithBudget(deadlineObserver{kind: tc.kind}, Options{ExecTimeout: 2})
			out, err := n.Execute(context.Background(), budgetTestContext{config: tc.config}, nil)
			require.NoError(t, err)
			remaining := out["out"][0].Payload.(time.Duration)
			assert.InDelta(t, tc.seconds, remaining.Seconds(), 0.1)
		})
	}
}

// TestBudgetDeclaredTimeoutDefault verifies a node whose configuration omits
// its timeout is budgeted from the default its timeout field declares, the
// value the node itself falls back to. It pins the regression where an Ask
// dialog saved without a timeout was cut off at the agent default plus grace,
// long before its own 120 second wait could report timed_out.
func TestBudgetDeclaredTimeoutDefault(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		declared     any
		seconds      float64
	}{
		{"numeric seconds default", `{}`, 120, 130},
		{"duration default", `{}`, "5s", 15},
		{"zero timeout uses the default", `{"timeout":0}`, 120, 130},
		{"configured timeout wins", `{"timeout":3}`, 120, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := WithBudget(deadlineObserver{timeoutDefault: tc.declared}, Options{ExecTimeout: 2})
			out, err := n.Execute(context.Background(), budgetTestContext{config: tc.config}, nil)
			require.NoError(t, err, "the observer node never fails")
			remaining := out["out"][0].Payload.(time.Duration)
			assert.InDelta(t, tc.seconds, remaining.Seconds(), 0.1,
				"the wrapper deadline must be the node's own timeout plus the cleanup grace")
		})
	}
}
