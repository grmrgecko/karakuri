package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSwitchNodeExpressionMode covers what an expression rule counts as true.
// An expression may return anything, so the truthiness table is the contract an
// author writes rules against — and it is not Go's: an empty string, zero, and
// nil are falsy, which is what someone coming from JavaScript expects and what
// the docs promise.
func TestSwitchNodeExpressionMode(t *testing.T) {
	msg := &engine.Msg{
		Payload: map[string]any{"temperature": 21.5, "room": "kitchen", "list": []any{1}},
		Topic:   "home/kitchen",
	}
	// Each case is one expression rule plus an otherwise branch, so a truthy
	// result takes "match" and a falsy one takes "else".
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"boolean true", `payload.temperature > 21`, "match"},
		{"boolean false", `payload.temperature > 30`, "else"},
		{"non-empty string is truthy", `payload.room`, "match"},
		{"non-zero number is truthy", `payload.temperature`, "match"},
		{"zero is falsy", `payload.temperature - payload.temperature`, "else"},
		{"non-empty list is truthy", `payload.list`, "match"},
		{"nil is falsy", `payload.missing`, "else"},
		{"topic access", `topic == \"home/kitchen\"`, "match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := `{"rules":[{"id":"match","operator":"expression","value":"` + tc.expr +
				`"},{"id":"else","operator":"otherwise"}]}`
			ec := newTestCtx(cfg, nil)
			out, err := SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err, "a valid expression must evaluate")
			assert.Truef(t, sameMessage(only(out, tc.want), msg),
				"the message must leave on the %q branch: %v", tc.want, out)
		})
	}
}

// TestSwitchNodeExpressionErrors pairs with the truthiness table above: an
// expression the node cannot evaluate fails rather than counting as false.
// Counting as false would route every message down the otherwise branch, which
// looks like a working switch whose condition is simply never met.
func TestSwitchNodeExpressionErrors(t *testing.T) {
	msg := &engine.Msg{Payload: map[string]any{}}

	ec := newTestCtx(`{"rules":[{"id":"r1","operator":"expression"}]}`, nil)
	_, err := SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	assert.Error(t, err, "a rule with no expression has nothing to evaluate and must not read as false")

	ec = newTestCtx(`{"rules":[{"id":"r1","operator":"expression","value":"payload.["}]}`, nil)
	_, err = SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	assert.Error(t, err,
		"an expression that will not compile must fail the node, not quietly route every message the other way")
}

// TestSwitchNodeRulesModeStillDefault covers a switch saved before the mode
// field existed. Graph documents outlive the build that wrote them, so a config
// with no mode has to keep behaving as it did — including routing to the true
// handle its edges are still wired to.
func TestSwitchNodeRulesModeStillDefault(t *testing.T) {
	ec := newTestCtx(`{"property":"payload.room","operator":"eq","value":"kitchen"}`, nil)
	msg := &engine.Msg{Payload: map[string]any{"room": "kitchen"}}
	out, err := SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "a config with no mode must still run")
	assert.Truef(t, sameMessage(only(out, "true"), msg),
		"an absent mode means rules, and the match routes to the true handle its edges name: %v", out)
}

// TestTemplateNodeString covers the plain rendering: the result replaces the
// payload and the rest of the message rides along, so a template that built a
// label does not also have to rebuild the topic it was addressed to.
func TestTemplateNodeString(t *testing.T) {
	ec := newTestCtx(`{"template":"Temp is {{ payload.temperature }}C in {{ payload.room }}"}`, nil)
	msg := &engine.Msg{
		Payload: map[string]any{"temperature": 21.5, "room": "office"},
		Topic:   "home/office",
		Meta:    map[string]any{"k": "v"},
	}
	out, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "a template over an available payload must render")

	res := only(out, "out")
	assert.Equal(t, "Temp is 21.5C in office", res.Payload, "both segments must be interpolated from the message")
	assert.Equal(t, "home/office", res.Topic, "the topic rides along, so a template need not rebuild the envelope")
	assert.Equal(t, "v", res.Meta["k"], "metadata rides along too")
}

// TestTemplateNodeJSONOutput covers building a structured payload from a
// template, which is how a command document is assembled for a device. The
// number must come back as a number rather than the text that was rendered.
func TestTemplateNodeJSONOutput(t *testing.T) {
	ec := newTestCtx(`{"template":"{\"value\": {{ payload.n }}, \"label\": \"{{ payload.s }}\"}","output_format":"json"}`, nil)
	msg := &engine.Msg{Payload: map[string]any{"n": 7.0, "s": "hi"}}
	out, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "a template rendering valid JSON must parse")

	obj, ok := only(out, "out").Payload.(map[string]any)
	require.Truef(t, ok, "expected a parsed object, got %T", only(out, "out").Payload)
	assert.Equal(t, float64(7), obj["value"], "the interpolated number must stay a number after the parse")
	assert.Equal(t, "hi", obj["label"], "the interpolated string must land in the right field")
}

// TestTemplateNodeRejections covers the two configs a template refuses, beside
// the renderings above, and the retry policy that goes with them. Rendering is
// pure computation over a message that already arrived, so retrying it would
// produce the identical failure every time — which is why the node declares
// itself not retryable rather than leaving an author to wait out three attempts
// at a typo.
func TestTemplateNodeRejections(t *testing.T) {
	ec := newTestCtx(`{"template":"not json {{ payload.s }}","output_format":"json"}`, nil)
	_, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: map[string]any{"s": "x"}}})
	assert.Error(t, err, "the port declared an object, so text that is not JSON must fail rather than pass as a string")

	_, err = TemplateNode{}.Execute(context.Background(), newTestCtx(`{}`, nil), map[string]*engine.Msg{"in": {}})
	assert.Error(t, err, "a node with no template has nothing to render and must say so")

	assert.False(t, TemplateNode{}.Meta().Retryable,
		"rendering is pure computation over a message that already arrived, so a retry would fail identically")
}

// TestScheduleConfigValidation covers the specs a schedule accepts and
// refuses. Both halves matter: the accepted rows are real cron shapes an author
// will type, including the six-field form with seconds and the @descriptors,
// and the refusals are the ways a schedule silently never fires if it is not
// caught at deploy.
func TestScheduleConfigValidation(t *testing.T) {
	valid := []scheduleConfig{
		{Cron: "0 8 * * *"},
		{Cron: "*/5 * * * *"},
		{Cron: "*/10 * * * * *"}, // Optional seconds field.
		{Cron: "@hourly"},
		{Cron: "0 8 * * *", Location: "Europe/Berlin"},
	}
	for _, c := range valid {
		assert.NoErrorf(t, c.validate(), "spec %q in zone %q is a real schedule and must be accepted", c.Cron, c.Location)
	}
	invalid := []scheduleConfig{
		{},
		{Cron: "not a cron"},
		{Cron: "* * * *"},
		{Cron: "0 8 * * *", Location: "Mars/Olympus"},
	}
	for _, c := range invalid {
		assert.Errorf(t, c.validate(),
			"spec %q in zone %q must be refused at deploy, or the schedule silently never fires", c.Cron, c.Location)
	}
}

// TestScheduleStartInvalidSpecFails covers arming a schedule that cannot be
// parsed. It has to fail when the flow is enabled rather than sit armed and
// never tick, since an armed trigger that never fires is indistinguishable from
// one whose time has not come.
func TestScheduleStartInvalidSpecFails(t *testing.T) {
	ec := newTestCtx(`{"cron":"bogus"}`, nil)
	err := ScheduleNode{}.Start(context.Background(), ec, func(*engine.Msg) {})
	assert.Error(t, err, "arming must fail at enable time, not leave a trigger that quietly never ticks")
}

// TestScheduleStartTicksAndStops drives the armed trigger against real wall
// time — the cron library schedules against the real clock, so this is one of
// the few places a test has to wait. A per-second spec ticks within 1.5s, and
// cancelling has to stop it promptly, since that is what disabling a flow does.
func TestScheduleStartTicksAndStops(t *testing.T) {
	ec := newTestCtx(`{"cron":"* * * * * *"}`, nil)
	ticks := make(chan *engine.Msg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (ScheduleNode{}).Start(ctx, ec, func(m *engine.Msg) { ticks <- m })
	}()

	// A per-second schedule must tick within 1.5s of wall time.
	select {
	case m := <-ticks:
		payload, ok := m.Payload.(map[string]any)
		require.Truef(t, ok, "a tick is an object a flow can read: %v", m.Payload)
		assert.Contains(t, payload, "scheduled_at",
			"the tick must say when it was scheduled for, which is the only content it carries")
		assert.Equal(t, "* * * * * *", m.Meta["cron"], "the tick names the spec that produced it")
	case <-time.After(1500 * time.Millisecond):
		require.FailNow(t, "a per-second schedule did not tick within 1.5 seconds")
	}

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "cancelling an armed trigger is a clean stop, not a failure")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "Start did not return after cancellation, so disabling a flow would hang")
	}
}

// TestScheduleExecuteSynthesizesTestTick covers what a test run gets from a
// schedule. An author cannot wait until 08:00 in the editor, so the node
// synthesizes a tick of the same shape a real one has; a real tick must pass
// through untouched, or a deployed flow would act on invented data.
func TestScheduleExecuteSynthesizesTestTick(t *testing.T) {
	ec := newTestCtx(`{"cron":"0 8 * * *"}`, nil)
	// A test run arrives with an empty payload.
	initial := &engine.Msg{Meta: map[string]any{"source": "test"}}
	out, err := ScheduleNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: initial})
	require.NoError(t, err, "a test run must produce a stand-in tick")

	msg := only(out, "out")
	payload, ok := msg.Payload.(map[string]any)
	require.Truef(t, ok, "the stand-in tick is an object: %v", msg.Payload)
	assert.Contains(t, payload, "scheduled_at",
		"the stand-in must have the same shape a real tick does, or rules are written against a payload that never arrives")
	assert.Equal(t, "0 8 * * *", msg.Meta["cron"], "the stand-in names the configured spec")
	assert.Equal(t, "test", msg.Meta["source"], "the seed message's metadata must carry over")

	assert.True(t, ScheduleNode{}.Meta().TestWithoutParams,
		"a schedule has nothing to seed, so the editor must be able to test-run it with no input")

	// A real tick passes through untouched.
	fired := &engine.Msg{Payload: map[string]any{"scheduled_at": "2026-01-01T00:00:00Z"}}
	out, err = ScheduleNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: fired})
	require.NoError(t, err, "a real tick must pass through the node")
	assert.Same(t, fired, only(out, "out"),
		"a real tick must not be replaced by the stand-in, or a deployed flow acts on invented data")
}

// TestWebhookExecuteSynthesizesTestRequest covers a test run against a webhook,
// which has the same problem a schedule does — there is no request — plus one
// more: the author may want to supply a body. A seeded payload becomes the
// stand-in request's body while the rest of the request detail is still filled
// in, so a rule reading meta.path works in the editor as it will in production.
func TestWebhookExecuteSynthesizesTestRequest(t *testing.T) {
	ec := newTestCtx(`{"token":"abc","methods":"ANY"}`, nil)
	// A test run arrives with an empty payload; the node stands in for a request.
	initial := &engine.Msg{Meta: map[string]any{"source": "test"}}
	out, err := WebhookNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: initial})
	require.NoError(t, err, "a test run must produce a stand-in request")

	msg := only(out, "out")
	payload, ok := msg.Payload.(map[string]any)
	require.Truef(t, ok, "the stand-in body is an object: %v", msg.Payload)
	assert.Empty(t, payload, "with nothing seeded the stand-in has an empty body, not an invented one")
	assert.Equal(t, "POST", msg.Meta["method"], "the stand-in has to look like a request a rule could branch on")
	assert.Equal(t, "/hook/abc", msg.Meta["path"], "the path is built from the configured token, as the real one is")
	assert.Equal(t, "test", msg.Meta["source"], "the seed message's metadata must carry over")

	// The methods field is a token list and meta.method is what a switch
	// routes on, so the stand-in names one method a real request could carry.
	for methods, want := range map[string]string{"GET,POST": "GET", "ANY,GET": "POST", "": "POST"} {
		several, err := WebhookNode{}.Execute(context.Background(),
			newTestCtx(`{"token":"abc","methods":"`+methods+`"}`, nil), map[string]*engine.Msg{})
		require.NoErrorf(t, err, "methods %q: a test run must produce a stand-in request", methods)
		assert.Equalf(t, want, only(several, "out").Meta["method"],
			"methods %q: meta.method must be a single method, not the whole list", methods)
	}

	assert.True(t, WebhookNode{}.Meta().TestWithoutParams,
		"a webhook can be test-run with no input, since the node stands in for the request")

	// A configured test body replaces the payload of the stand-in request, and
	// the rest of the request detail is still there.
	seeded := &engine.Msg{
		Payload: map[string]any{"state": "ON"},
		Meta:    map[string]any{"source": "test"},
	}
	out, err = WebhookNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: seeded})
	require.NoError(t, err, "a seeded test run must produce a stand-in request carrying the body")

	msg = only(out, "out")
	payload, ok = msg.Payload.(map[string]any)
	require.Truef(t, ok, "the seeded body is an object: %v", msg.Payload)
	assert.Equal(t, "ON", payload["state"], "the seeded body must become the request's payload")
	assert.Equal(t, "/hook/abc", msg.Meta["path"], "the rest of the request detail must still be filled in")
	assert.JSONEq(t, `{"state":"ON"}`, msg.Meta["body"].(string),
		"the raw body must be there too, for a rule that reads it rather than the parsed payload")

	// A real request passes through untouched.
	fired := &engine.Msg{Payload: map[string]any{"state": "ON"}}
	out, err = WebhookNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: fired})
	require.NoError(t, err, "a real request must pass through the node")
	assert.Same(t, fired, only(out, "out"),
		"a real request must not be replaced by the stand-in, or a deployed webhook acts on invented data")
}

// TestWebhookNodeStartRegistersToken covers the lifecycle of the token behind
// /hook/{token}. Arming registers it and cancelling removes it, which is what
// enabling and disabling a flow does — a token left behind after a disable
// would keep answering requests for a flow that is no longer running.
func TestWebhookNodeStartRegistersToken(t *testing.T) {
	ec := newTestCtx(`{"token":"unit-test-token","methods":"ANY"}`, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (WebhookNode{}).Start(ctx, ec, func(*engine.Msg) {})
	}()

	// The token must appear on the shared registry the HTTP route reads.
	deadline := time.After(2 * time.Second)
	for {
		if _, found, allowed := engine.Webhooks().Resolve("unit-test-token", "GET"); found && allowed {
			break
		}
		select {
		case <-deadline:
			require.FailNow(t, "the token never reached the registry, so /hook would answer 404 for an enabled flow")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	require.NoError(t, <-done, "cancelling an armed webhook is a clean stop, not a failure")

	_, found, _ := engine.Webhooks().Resolve("unit-test-token", "GET")
	assert.False(t, found,
		"the token must be gone once the trigger stops, or a disabled flow keeps answering requests")

	err := WebhookNode{}.Start(context.Background(), newTestCtx(`{}`, nil), func(*engine.Msg) {})
	assert.Error(t, err, "a webhook with no token has no route to register and must fail at enable time")
}
