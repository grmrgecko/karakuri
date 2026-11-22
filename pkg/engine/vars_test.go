package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunVarsShareAcrossNodes covers the point of run variables: a node writes
// one, a node on an unconnected branch reads it back, and expressions see it
// under `vars`.
func TestRunVarsShareAcrossNodes(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var seen any
	var evaluated any
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "writer", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				ec.SetVar("ticket", "abc-123")
				return passthrough("out")(ctx, ec, in)
			}},
		&stubNode{typ: "reader", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				seen, _ = ec.GetVar("ticket")
				evaluated, _ = ec.Eval("vars.ticket", in["in"])
				return passthrough("out")(ctx, ec, in)
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("w", "writer"), gnode("r", "reader")},
		[]flow.Edge{gedge("t", "out", "w", "in"), gedge("w", "out", "r", "in")},
	)

	require.Equal(t, engine.RunStatusSucceeded, waitDone(t, startRun(t, e, g)).Status,
		"both nodes must run for the read to mean anything")

	assert.Equal(t, "abc-123", seen, "a run variable written by one node must be readable by another")
	assert.Equal(t, "abc-123", evaluated, "the same value must be reachable from an expression as vars.ticket")
}

// TestRunVarsAreScopedToTheRun proves two runs of the same flow never see each
// other's variables, which is what makes them safe for concurrent webhooks.
func TestRunVarsAreScopedToTheRun(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var priorSeen []any
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "counter", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				prior, _ := ec.GetVar("count")
				priorSeen = append(priorSeen, prior)
				ec.SetVar("count", 1)
				return passthrough("out")(ctx, ec, in)
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("c", "counter")},
		[]flow.Edge{gedge("t", "out", "c", "in")},
	)
	flowID := uuid.New()

	for i := 0; i < 2; i++ {
		h := startRun(t, e, g, func(ro *engine.RunOptions) { ro.FlowID = flowID })
		require.Equalf(t, engine.RunStatusSucceeded, waitDone(t, h).Status, "run %d must complete", i)
	}
	for i, prior := range priorSeen {
		assert.Nilf(t, prior, "run %d must start with no variables from the run before it: two concurrent webhooks would otherwise read each other's state", i)
	}
}

// TestSetVarNilClears covers clearing a variable, the only way a script can
// remove one once set.
func TestSetVarNilClears(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var present bool
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "clear", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				ec.SetVar("gone", "here")
				ec.SetVar("gone", nil)
				_, present = ec.GetVar("gone")
				return passthrough("out")(ctx, ec, in)
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("c", "clear")},
		[]flow.Edge{gedge("t", "out", "c", "in")},
	)
	waitDone(t, startRun(t, e, g))
	assert.False(t, present, "writing nil is the only way a script can remove a variable, so it must actually remove it")
}

// TestRunEndAnswersPendingWebhook covers the completion path: a run whose
// trigger message carries a pending request ID answers it when the run ends,
// using the response variables the flow set.
func TestRunEndAnswersPendingWebhook(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "respond", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				ec.SetVar(engine.VarResponseCode, 201)
				ec.SetVar(engine.VarResponseBody, map[string]any{"ok": true})
				ec.SetVar(engine.VarResponseHeaders, map[string]any{"X-Flow": "yes"})
				return passthrough("out")(ctx, ec, in)
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("r", "respond")},
		[]flow.Edge{gedge("t", "out", "r", "in")},
	)

	responses, release := engine.Responses().Register("req-vars")
	defer release()
	h := startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.InitialMsg = &engine.Msg{Meta: map[string]any{engine.RequestIDMeta: "req-vars"}}
	})
	waitDone(t, h)

	select {
	case resp := <-responses:
		assert.Equal(t, 201, resp.Status, "the response_code variable is what sets the status when no node does")
		assert.JSONEq(t, `{"ok":true}`, resp.Body, "the response_body variable is encoded as the reply")
		assert.Equal(t, "yes", resp.Headers["X-Flow"], "the response_headers variable must be merged into the reply")
	case <-time.After(waitTimeout):
		require.FailNow(t, "the run ended without answering the request waiting on it, which leaves the caller hanging")
	}
}

// TestRunEndAnswersWithRunStatus covers the default reply for a flow that set
// no response variables: a failed run reports itself rather than timing out.
func TestRunEndAnswersWithRunStatus(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "boom", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("boom")
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("b", "boom")},
		[]flow.Edge{gedge("t", "out", "b", "in")},
	)

	responses, release := engine.Responses().Register("req-default")
	defer release()
	h := startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.InitialMsg = &engine.Msg{Meta: map[string]any{engine.RequestIDMeta: "req-default"}}
	})
	waitDone(t, h)

	select {
	case resp := <-responses:
		assert.Equal(t, 500, resp.Status, "a flow that set no response variables and failed must answer 500, not hang")

		var doc map[string]any
		require.NoErrorf(t, json.Unmarshal([]byte(resp.Body), &doc), "the default reply must be JSON: %q", resp.Body)
		assert.Equal(t, engine.RunStatusFailed, doc["status"],
			"the caller has no other way to learn the run failed")
	case <-time.After(waitTimeout):
		require.FailNow(t, "a failed run must still answer the request waiting on it")
	}
}

// TestRunEndLeavesAnsweredRequestAlone proves the completion responder never
// overwrites a reply a Webhook return already delivered.
func TestRunEndLeavesAnsweredRequestAlone(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "early", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				engine.Responses().Respond("req-early", &engine.HTTPResponse{Status: 202, Body: "early"})
				return passthrough("out")(ctx, ec, in)
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("e", "early")},
		[]flow.Edge{gedge("t", "out", "e", "in")},
	)

	responses, release := engine.Responses().Register("req-early")
	defer release()
	h := startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.InitialMsg = &engine.Msg{Meta: map[string]any{engine.RequestIDMeta: "req-early"}}
	})
	waitDone(t, h)

	resp := <-responses
	assert.Equal(t, 202, resp.Status, "the reply a Webhook return already sent must stand")
	assert.Equal(t, "early", resp.Body, "the reply a Webhook return already sent must stand")

	select {
	case second := <-responses:
		assert.Fail(t, "run end sent a second response over one already delivered", "%+v", second)
	default:
	}
}

// TestResponseFromVarsIgnoresJunk covers the guards on variable values: an
// out-of-range status and a non-map header set leave the base response alone.
func TestResponseFromVarsIgnoresJunk(t *testing.T) {
	base := &engine.HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Body: "base"}
	got := engine.ResponseFromVars(map[string]any{
		engine.VarResponseCode:    99,
		engine.VarResponseHeaders: "not-a-map",
	}, base)
	assert.Equal(t, 200, got.Status, "99 is not a status code, so the base status stands rather than a nonsense reply going out")
	assert.Equal(t, "text/plain", got.Headers["Content-Type"], "a headers variable that is not a map leaves the base headers alone")
	assert.Equal(t, "base", got.Body, "junk in one variable must not blank the body")

	// A string body passes through untouched so a flow can reply with HTML.
	got = engine.ResponseFromVars(map[string]any{
		engine.VarResponseCode: float64(404),
		engine.VarResponseBody: "<h1>nope</h1>",
	}, base)
	assert.Equal(t, 404, got.Status, "a status arriving as a float64, which is what JSON decoding produces, must still apply")
	assert.Equal(t, "<h1>nope</h1>", got.Body, "a string body passes through untouched so a flow can reply with HTML")

	assert.Equal(t, 200, base.Status, "the base response is shared and must not be written through")
	assert.Equal(t, "base", base.Body, "the base response is shared and must not be written through")
}
