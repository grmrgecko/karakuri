package homeassistant

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testToken = "test-token"

// newTestDriver builds a driver against a fake server with short reconnect
// and ping timings suitable for tests. It is not started.
func newTestDriver(t *testing.T, baseURL, token string) *Driver {
	t.Helper()
	cfg, _ := json.Marshal(Config{BaseURL: baseURL, Token: token})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err, "the driver must build from a valid config")

	driver, ok := d.(*Driver)
	require.True(t, ok, "the homeassistant kind's factory must produce its driver")
	driver.initialBackoff = 50 * time.Millisecond
	return driver
}

// startTestDriver builds and starts a driver, waiting for the connected state.
func startTestDriver(t *testing.T, srv *hatest.Server) *Driver {
	t.Helper()
	driver := newTestDriver(t, srv.URL, srv.Token)
	require.NoError(t, driver.Start(context.Background()), "the driver must start")

	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver never authenticated against the fake Home Assistant")
	return driver
}

// waitFor polls a condition until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, msg)
}

// TestWebsocketURL pins how an operator's base URL becomes the websocket
// endpoint Home Assistant actually listens on. The rows are the shapes people
// really paste in — with and without a scheme, with and without a trailing
// slash, and behind a reverse proxy on a subpath — because getting any of them
// wrong means the connection simply never establishes, with a network error as
// the only clue.
func TestWebsocketURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "http://ha.local:8123", want: "ws://ha.local:8123/api/websocket"},
		{in: "http://ha.local:8123/", want: "ws://ha.local:8123/api/websocket"},
		{in: "https://ha.example.com", want: "wss://ha.example.com/api/websocket"},
		{in: "ha.local:8123", want: "ws://ha.local:8123/api/websocket"},
		{in: "ws://ha.local:8123", want: "ws://ha.local:8123/api/websocket"},
		{in: "https://proxy.example.com/ha/", want: "wss://proxy.example.com/ha/api/websocket"},
		{in: "", wantErr: true},
		{in: "ftp://ha.local", wantErr: true},
	}
	for _, c := range cases {
		got, err := WebsocketURL(c.in)
		if c.wantErr {
			assert.Errorf(t, err, "%q names no Home Assistant to connect to and must be refused", c.in)
			continue
		}
		if assert.NoErrorf(t, err, "%q is a shape an operator really pastes in and must resolve", c.in) {
			// https becomes wss and http becomes ws, and the API path is
			// appended to whatever subpath the base URL already carries.
			assert.Equalf(t, c.want, got, "WebsocketURL(%q)", c.in)
		}
	}
}

// TestDriverConnectAndStop covers the connection lifecycle against a fake
// server speaking the real websocket protocol. One connection is opened rather
// than several, and stopping reports disabled rather than error — an operator
// turning a connection off is not a fault and must not raise an alert.
func TestDriverConnectAndStop(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()

	driver := startTestDriver(t, srv)
	assert.Equal(t, 1, srv.ConnCount(),
		"one connection is multiplexed for everything, so a second would mean the driver is reconnecting in a loop")

	driver.Stop()
	assert.Equal(t, integrations.StateDisabled, driver.Status().State,
		"stopping is an operator turning the connection off, which must not read as a fault")
}

// TestDriverAuthInvalid covers the commonest configuration mistake: a long-lived
// access token that was mistyped or has expired. The error has to name
// authentication, since the connections page shows that message and "connection
// failed" would send the operator looking at their network.
func TestDriverAuthInvalid(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()

	driver := newTestDriver(t, srv.URL, "wrong-token")
	require.NoError(t, driver.Start(context.Background()),
		"starting must not fail synchronously: connecting happens in the background so the server can boot")
	t.Cleanup(driver.Stop)

	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateError
	}, "a rejected token never settled into an error state")

	assert.Contains(t, driver.Status().Error, "authentication failed",
		"the operator sees this message; a generic connection error would send them looking at their network")
}

// TestDriverCallService covers the command path a dashboard row and an
// ha-call-service node both use. The recorded call is checked on the server
// side, so the assertion is that Home Assistant received what was asked for
// rather than that the driver believed it sent it — data and target travel as
// separate fields in the protocol, and swapping them is silently accepted by a
// client-side check.
//
// The scripted failure is the other half: Home Assistant reports a service
// error in its reply rather than at the transport, so a driver that only
// watched for transport errors would report every failed device call as a
// success.
func TestDriverCallService(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	driver := startTestDriver(t, srv)

	// Successful call with data and target.
	result, err := driver.CallService(context.Background(), "light", "turn_on",
		map[string]any{"brightness": 128},
		map[string]any{"entity_id": []string{"light.kitchen"}})
	require.NoError(t, err, "a service call to a live server must succeed")
	assert.NotNil(t, result, "the reply carries what Home Assistant did, which a flow may act on")

	calls := srv.ServiceCalls()
	require.Len(t, calls, 1, "exactly one call must reach the server")
	assert.Equal(t, "light", calls[0].Domain, "the domain must reach the server as sent")
	assert.Equal(t, "turn_on", calls[0].Service, "the service must reach the server as sent")
	assert.Equal(t, float64(128), calls[0].ServiceData["brightness"],
		"service data is a separate protocol field from the target, and swapping them is only visible server-side")
	assert.Equal(t, []any{"light.kitchen"}, calls[0].Target["entity_id"],
		"the target must arrive as its own field, or Home Assistant acts on nothing")

	// Home Assistant reports a service failure in its reply, not at the
	// transport, so this is the path a driver watching only for network errors
	// would report as a success.
	srv.FailService("switch", "turn_off", 1, "device unreachable")
	_, err = driver.CallService(context.Background(), "switch", "turn_off", nil, nil)
	require.Error(t, err, "a failure reported in the reply must become a Go error, not a silent success")
	assert.Contains(t, err.Error(), "device unreachable",
		"Home Assistant's own message must survive: it is the only thing that says which device failed")

	_, err = driver.CallService(context.Background(), "switch", "turn_off", nil, nil)
	assert.NoError(t, err, "one failed call must not poison the session for the calls after it")
}

// TestDriverCallServiceSlowReply pins a regression with a real cause: Home
// Assistant answers call_service only once the service handler has finished
// driving the devices, so turning on a group of lights can take seconds. Under
// the generic command timeout that reply arrived too late and a working light
// group reported a failure — while the lights came on.
//
// Service calls therefore run on their own, longer budget. The three cases are
// the ones that must stay distinguishable: a slow reply that still arrives, one
// that blows the budget, and a caller that gave up. A timeout and a
// cancellation reported the same way would send an operator hunting for a
// device fault that never happened.
func TestDriverCallServiceSlowReply(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	driver := newTestDriver(t, srv.URL, srv.Token)
	driver.cmdTimeout = 100 * time.Millisecond
	driver.svcTimeout = 5 * time.Second
	require.NoError(t, driver.Start(context.Background()), "the driver must start")
	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver never connected")

	// 500ms is five times the generic command timeout and a tenth of the
	// service budget, so only the right one can let it through.
	srv.SlowService("light", "turn_on", 500*time.Millisecond)
	_, err := driver.CallService(context.Background(), "light", "turn_on", nil,
		map[string]any{"entity_id": "light.lamps"})
	require.NoError(t, err,
		"a slow reply must be waited out on the service budget, or a working light group reports a failure")

	driver.svcTimeout = 100 * time.Millisecond
	_, err = driver.CallService(context.Background(), "light", "turn_on", nil, nil)
	require.Error(t, err, "past its own budget the call must fail rather than wait forever")
	assert.Contains(t, err.Error(), "timed out after",
		"the error must say a budget was blown, so the operator raises it rather than hunting a device fault")

	// A caller that gave up is reported as a cancellation, not a timeout.
	ctx, cancel := context.WithCancel(context.Background())
	driver.svcTimeout = 5 * time.Second
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err = driver.CallService(ctx, "light", "turn_on", nil, nil)
	require.Error(t, err, "a cancelled call must not be reported as a success")
	assert.Contains(t, err.Error(), "canceled",
		"a cancelled run is not a slow device, and reporting it as a timeout sends the operator after the wrong thing")
}

// TestDriverGetStatesAndCache covers the two ways entity state is read, and why
// there are two. A dashboard drawing thirty entity rows would issue thirty
// get_states calls over one websocket; the cache collapses that into one, which
// is what the short TTL is for. A flow node needs the opposite — the value as it
// is right now — so the live call must never be served from the cache.
func TestDriverGetStatesAndCache(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	srv.SetState("light.kitchen", "on", map[string]any{"friendly_name": "Kitchen Light"})
	srv.SetState("switch.zigbee_plug", "off", nil)
	driver := startTestDriver(t, srv)

	entities, err := driver.GetStates(context.Background())
	require.NoError(t, err, "reading state from a connected server must succeed")
	require.Len(t, entities, 2, "both entities the server holds must come back")

	assert.Equal(t, "light.kitchen", entities[0].EntityID, "the entity id is how everything downstream addresses it")
	assert.Equal(t, "on", entities[0].State, "the state is what a rule branches on")
	assert.Equal(t, "Kitchen Light", entities[0].Attributes["friendly_name"],
		"attributes must come through too: a picker shows the friendly name rather than the id")

	cached, err := driver.StatesCached(context.Background())
	require.NoError(t, err, "the cached read must succeed")
	require.Len(t, cached, 2, "the first cached read fetches what the live one did")

	// The server gains an entity the cache cannot know about.
	srv.SetState("sensor.new", "42", nil)

	cached, err = driver.StatesCached(context.Background())
	require.NoError(t, err, "the second cached read must succeed")
	assert.Len(t, cached, 2,
		"within the TTL the cache is reused, which is what stops a thirty-row dashboard issuing thirty calls")

	entities, err = driver.GetStates(context.Background())
	require.NoError(t, err, "the live read must succeed")
	assert.Len(t, entities, 3,
		"a live read must never be served from the cache: a flow node needs the value as it is right now")
}

// TestDriverSubscribeFanoutAndResubscribe covers the sharing and the recovery
// together, because they are the same mechanism seen twice. Many flows subscribe
// to state_changed, and one websocket subscription serves them all — so the
// driver fans each event out itself, and a reconnection has to reestablish the
// subscription or every one of those flows goes silently deaf while the
// connection reports healthy. Unsubscribing one handler must leave the rest
// running, which is the case a shared subscription gets wrong.
func TestDriverSubscribeFanoutAndResubscribe(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	driver := startTestDriver(t, srv)

	// Two handlers for the same event type share one server subscription.
	var mu sync.Mutex
	var got1, got2 []string
	unsub1, err := driver.SubscribeEvents("state_changed", func(event json.RawMessage) {
		mu.Lock()
		got1 = append(got1, string(event))
		mu.Unlock()
	})
	require.NoError(t, err, "the first handler must subscribe")
	_, err = driver.SubscribeEvents("state_changed", func(event json.RawMessage) {
		mu.Lock()
		got2 = append(got2, string(event))
		mu.Unlock()
	})
	require.NoError(t, err, "a second handler for the same event must subscribe too")

	waitFor(t, 5*time.Second, func() bool { return srv.SubscribeCount("state_changed") == 1 },
		"the driver never subscribed on the server")
	assert.Equal(t, 1, srv.SubscribeCount("state_changed"),
		"two handlers must share one server subscription, not open one each")

	// One pushed event reaches both handlers.
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "off", nil))
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got1) == 1 && len(got2) == 1
	}, "one pushed event must reach every handler, since they share one subscription")

	// A server-forced disconnect reconnects and resubscribes automatically.
	srv.CloseClients()
	waitFor(t, 5*time.Second, func() bool {
		return srv.ConnCount() == 2 && srv.SubscribeCount("state_changed") == 2 &&
			driver.Status().State == integrations.StateConnected
	}, "the driver did not reconnect and resubscribe after the server dropped it")

	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "off", nil),
		hatest.StateObj("light.kitchen", "on", nil))
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got1) == 2 && len(got2) == 2
	}, "the session came back but the subscription did not: every flow goes deaf while the status reads healthy")

	// After one handler unsubscribes the other still receives events.
	unsub1()
	time.Sleep(50 * time.Millisecond)
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "off", nil))
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got2) == 3
	}, "removing one handler must not tear down the shared subscription the others depend on")

	mu.Lock()
	assert.Len(t, got1, 2, "the unsubscribed handler must stop receiving")
	mu.Unlock()
}

// TestDriverSubscribeStateChanges covers the typed wrapper the ha-state trigger
// uses. Home Assistant sends state_changed with the old and new state objects
// nested inside the event, and the wrapper unpacks them — the old value is what
// lets a trigger fire on a transition rather than on every report.
func TestDriverSubscribeStateChanges(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	driver := startTestDriver(t, srv)

	type change struct {
		entityID string
		oldState string
		newState string
	}
	var mu sync.Mutex
	var changes []change
	_, err := driver.SubscribeStateChanges(func(entityID string, oldState, newState json.RawMessage) {
		var oldObj, newObj struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(oldState, &oldObj)
		_ = json.Unmarshal(newState, &newObj)
		mu.Lock()
		changes = append(changes, change{entityID: entityID, oldState: oldObj.State, newState: newObj.State})
		mu.Unlock()
	})
	require.NoError(t, err, "the state-change handler must subscribe")
	waitFor(t, 5*time.Second, func() bool { return srv.SubscribeCount("state_changed") == 1 },
		"the driver never subscribed on the server")

	srv.PushStateChanged("switch.zigbee_plug",
		hatest.StateObj("switch.zigbee_plug", "on", nil),
		hatest.StateObj("switch.zigbee_plug", "off", nil))
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) == 1
	}, "the pushed state change never reached the handler")

	mu.Lock()
	c := changes[0]
	mu.Unlock()
	assert.Equal(t, "switch.zigbee_plug", c.entityID, "the entity id must be lifted out of the event")
	assert.Equal(t, "on", c.oldState,
		"the old state must be unpacked too, or a trigger cannot fire on a transition rather than every report")
	assert.Equal(t, "off", c.newState, "the new state is what the flow acts on")
}

// TestDriverPingTimeoutReconnect covers the failure a websocket does not
// report: a connection that is still open at the socket level but whose peer
// has stopped answering — a Home Assistant that hung, or a NAT table that
// dropped the mapping. Nothing errors, so without a ping the driver would sit
// there believing itself connected forever. Dropping pongs while leaving the
// socket open is the only way to produce exactly that.
func TestDriverPingTimeoutReconnect(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()

	driver := newTestDriver(t, srv.URL, srv.Token)
	driver.pingInterval = 100 * time.Millisecond
	driver.pingTimeout = 200 * time.Millisecond
	require.NoError(t, driver.Start(context.Background()), "the driver must start")
	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver never connected")

	srv.SetDropPings(true)
	waitFor(t, 5*time.Second, func() bool { return srv.ConnCount() >= 2 },
		"the socket stayed open but the peer stopped answering, and the driver never noticed")

	srv.SetDropPings(false)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver reconnected but never settled back into a connected state")
}

// TestTestConnection covers the Test button on the connection form. It has to
// connect and authenticate for real, since the two mistakes it exists to catch
// — a wrong address and a wrong token — both produce a config that validates
// perfectly. The distinct message for a bad token is what tells the operator
// which of the two they made.
func TestTestConnection(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()

	good, _ := json.Marshal(Config{BaseURL: srv.URL, Token: testToken})
	assert.NoError(t, TestConnection(context.Background(), uuid.New(), good),
		"a reachable server with a valid token must test clean, or the button is useless")

	badToken, _ := json.Marshal(Config{BaseURL: srv.URL, Token: "wrong"})
	err := TestConnection(context.Background(), uuid.New(), badToken)
	require.Error(t, err, "the test must authenticate, not merely open a socket")
	assert.Contains(t, err.Error(), "authentication failed",
		"a wrong token and a wrong address must be distinguishable, or the operator fixes the wrong field")

	// Port 1 is never listening.
	dead, _ := json.Marshal(Config{BaseURL: "http://127.0.0.1:1", Token: testToken})
	assert.Error(t, TestConnection(context.Background(), uuid.New(), dead),
		"an unreachable server must fail the test rather than pass on a valid-looking config")

	assert.Error(t, TestConnection(context.Background(), uuid.New(), []byte(`{}`)),
		"a config with no address names nothing to test")
}

// TestSubscribeDuringInFlightSubscribe locks that a handler registered while
// an earlier subscribe for its event type is still unanswered ends up with a
// server subscription. A flow redeployed while Home Assistant was slow to
// answer dropped its old trigger, whose unsubscribe took the pending
// subscription with it, and the new trigger found the command in flight and
// sent none, so it heard nothing until the next reconnect.
func TestSubscribeDuringInFlightSubscribe(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	srv.SlowSubscribe("doorbell", 300*time.Millisecond)
	driver := startTestDriver(t, srv)

	unsubscribe, err := driver.SubscribeEvents("doorbell", func(json.RawMessage) {})
	require.NoError(t, err, "the first handler must subscribe")
	waitFor(t, 5*time.Second, func() bool {
		driver.mu.Lock()
		defer driver.mu.Unlock()
		_, sent := driver.subIDs["doorbell"]
		return sent
	}, "the first subscribe command was never sent")

	unsubscribe()
	heard := make(chan struct{}, 1)
	_, err = driver.SubscribeEvents("doorbell", func(json.RawMessage) {
		select {
		case heard <- struct{}{}:
		default:
		}
	})
	require.NoError(t, err, "the second handler must subscribe")

	waitFor(t, 5*time.Second, func() bool {
		srv.PushEvent("doorbell", map[string]any{"event_type": "doorbell"})
		select {
		case <-heard:
			return true
		default:
			return false
		}
	}, "the handler registered during the in-flight subscribe never received an event")
}

// TestFetchFollowsTheBaseURLShape pins that an HTTP fetch reaches Home
// Assistant under every base URL shape the websocket accepts. A base URL
// written with ws:// or wss:// connected and then failed every camera image
// and stream, because the fetch reused it unchanged and HTTP has no such
// scheme. A path outside the API stays refused under each of them.
func TestFetchFollowsTheBaseURLShape(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetImage("/api/camera_proxy/camera.door", []byte("door-jpeg"))
	host := strings.TrimPrefix(srv.URL, "http://")

	for _, base := range []string{srv.URL, srv.URL + "/", host, "ws://" + host} {
		driver := newTestDriver(t, base, srv.Token)
		resp, err := driver.Fetch(context.Background(), "/api/camera_proxy/camera.door")
		if assert.NoErrorf(t, err, "a base URL of %q connects, so its images must load too", base) {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.NoError(t, readErr, "the image body must be readable")
			assert.Equalf(t, "door-jpeg", string(body), "the fetch under %q returns the image", base)
		}
		_, err = driver.Fetch(context.Background(), "/local/secret.txt")
		assert.Errorf(t, err, "a path outside /api/ is refused under %q", base)
	}
}
