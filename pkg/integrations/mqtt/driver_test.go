package mqtt

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startBroker runs an embedded in-process MQTT broker on a random port and
// returns its address.
func startBroker(t *testing.T) (*mochi.Server, string) {
	t.Helper()
	server := mochi.New(&mochi.Options{
		InlineClient: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil), "the test broker accepts any client")
	tcp := listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"})
	require.NoError(t, server.AddListener(tcp), "the test broker needs a loopback listener")
	go func() { _ = server.Serve() }()
	// Closing twice panics inside the broker, so tests that stop it early
	// share this guard with the cleanup.
	var once sync.Once
	stop := func() { once.Do(func() { _ = server.Close() }) }
	brokerStops.Store(server, stop)
	t.Cleanup(stop)
	return server, tcp.Address()
}

// brokerStops maps a test broker to its idempotent stop function.
var brokerStops sync.Map

// stopBroker shuts a test broker down early; the test cleanup tolerates it.
func stopBroker(t *testing.T, server *mochi.Server) {
	t.Helper()
	stop, ok := brokerStops.Load(server)
	require.True(t, ok, "the broker must have been started by startBroker to be stopped through it")
	stop.(func())()
}

// startDriver builds and starts a driver against the broker address, waiting
// for it to reach the connected state.
func startDriver(t *testing.T, addr string, statuses *[]integrations.Status) *Driver {
	t.Helper()
	var mu sync.Mutex
	cfg, _ := json.Marshal(Config{BrokerURL: "mqtt://" + addr})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{
		PublishStatus: func(s integrations.Status) {
			if statuses != nil {
				mu.Lock()
				*statuses = append(*statuses, s)
				mu.Unlock()
			}
		},
	})
	require.NoError(t, err, "the driver must build from a valid config")

	driver, ok := d.(*Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")
	require.NoError(t, driver.Start(context.Background()), "the driver must start")

	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver never connected to the test broker")
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

// TestDriverSubscribeDispatch covers the multiplexing the driver does over one
// broker session. Several flows subscribe to overlapping filters through one
// connection, so the driver has to route each message to exactly the handlers
// whose filter matches — and unsubscribing one must not disturb the others,
// which is the case a naive implementation gets wrong by tearing down the
// shared broker subscription.
func TestDriverSubscribeDispatch(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	// Two handlers on overlapping filters share the session.
	var mu sync.Mutex
	var wildcard, exact []string
	unsubWild, err := driver.Subscribe("zigbee/+/state", 0, func(topic string, payload []byte) {
		mu.Lock()
		wildcard = append(wildcard, topic+"="+string(payload))
		mu.Unlock()
	})
	require.NoError(t, err, "a wildcard filter must be subscribable")
	_, err = driver.Subscribe("zigbee/kitchen/state", 0, func(topic string, payload []byte) {
		mu.Lock()
		exact = append(exact, topic)
		mu.Unlock()
	})
	require.NoError(t, err, "an overlapping exact filter must be subscribable through the same session")

	// Give the async subscribes time to reach the broker, then publish.
	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"ON"}`), false, 0)
		mu.Lock()
		defer mu.Unlock()
		return len(wildcard) > 0 && len(exact) > 0
	}, "handlers did not receive the message")

	// A non-matching topic must not reach the wildcard handler.
	mu.Lock()
	wildBefore := len(wildcard)
	mu.Unlock()
	_ = server.Publish("other/topic", []byte("x"), false, 0)
	_ = server.Publish("zigbee/hall/state", []byte("y"), false, 0)
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(wildcard) > wildBefore
	}, "wildcard handler did not receive second message")
	mu.Lock()
	assert.NotContains(t, wildcard, "other/topic=x",
		"the handler must only see topics its own filter matches, not everything the session receives")
	mu.Unlock()

	// After unsubscribe the wildcard handler must stop receiving.
	unsubWild()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	wildAfter := len(wildcard)
	exactBefore := len(exact)
	mu.Unlock()
	_ = server.Publish("zigbee/kitchen/state", []byte("z"), false, 0)
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(exact) > exactBefore
	}, "exact handler did not receive message after wildcard unsubscribe")
	mu.Lock()
	assert.Len(t, wildcard, wildAfter, "an unsubscribed handler must stop receiving")
	mu.Unlock()
}

// TestDriverPublish round-trips a message through the real broker and back into
// the driver's own subscription, which is the only way to know the publish
// actually left the process rather than being handled internally.
func TestDriverPublish(t *testing.T) {
	_, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	// Round trip a publish through the broker back into a subscription.
	var mu sync.Mutex
	var received []byte
	_, err := driver.Subscribe("out/test", 0, func(topic string, payload []byte) {
		mu.Lock()
		received = append([]byte{}, payload...)
		mu.Unlock()
	})
	require.NoError(t, err, "the driver must be able to subscribe to the topic it publishes on")

	waitFor(t, 5*time.Second, func() bool {
		require.NoError(t, driver.Publish(context.Background(), "out/test", 0, false, []byte("hello")),
			"publishing to a connected broker must succeed")
		mu.Lock()
		defer mu.Unlock()
		return string(received) == "hello"
	}, "the published message never came back through the broker, so it did not leave the process")
}

// TestDriverStatusTransitions covers the status the connections page and the
// metrics both read. Stopping reports disabled rather than error, because an
// operator turning a connection off is not a fault and must not raise an alert.
func TestDriverStatusTransitions(t *testing.T) {
	_, addr := startBroker(t)
	var statuses []integrations.Status
	driver := startDriver(t, addr, &statuses)

	require.Equal(t, integrations.StateConnected, driver.Status().State,
		"a driver connected to a live broker must report itself connected")

	driver.Stop()
	assert.Equal(t, integrations.StateDisabled, driver.Status().State,
		"stopping is an operator turning the connection off, which must not read as a fault")
}

// TestDriverErrorStatus covers a broker that cannot be reached. Port 1 is never
// listening, so the driver has to settle into an error state carrying a message
// — the connections page shows that message, and without one the operator sees
// a red dot with nothing to act on.
func TestDriverErrorStatus(t *testing.T) {
	cfg, _ := json.Marshal(Config{BrokerURL: "mqtt://127.0.0.1:1", KeepAliveS: 1})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "bad", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err, "the driver must build even for an unreachable broker")

	driver, ok := d.(*Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")
	require.NoError(t, driver.Start(context.Background()),
		"starting must not fail synchronously: connecting happens in the background so the server can boot")

	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateError
	}, "an unreachable broker never settled into an error state")

	assert.NotEmpty(t, driver.Status().Error,
		"the connections page shows this message; without one the operator sees a red dot and nothing to act on")
}

// TestTestConnection covers the Test button on the connection form, which is
// how an operator finds out they typed the address wrong before saving. It has
// to actually connect — a check that only validated the config would pass for
// every broker that is not there, which is precisely the case it exists for.
func TestTestConnection(t *testing.T) {
	_, addr := startBroker(t)

	good, _ := json.Marshal(Config{BrokerURL: "mqtt://" + addr})
	assert.NoError(t, TestConnection(context.Background(), uuid.New(), good),
		"a live broker must test clean, or the button is useless")

	// Port 1 is never listening.
	bad, _ := json.Marshal(Config{BrokerURL: "mqtt://127.0.0.1:1"})
	assert.Error(t, TestConnection(context.Background(), uuid.New(), bad),
		"the test must actually connect: validating the config alone would pass for every unreachable broker")

	assert.Error(t, TestConnection(context.Background(), uuid.New(), []byte(`{}`)),
		"a config with no broker address names nothing to test")
}

// TestDispatchDoesNotBlockOnSlowHandler guards the invariant that makes the
// connection survivable: dispatch runs on paho's reader, which also processes
// PINGRESP, so it must hand off to the driver's goroutine and shed load rather
// than wait for a slow flow.
func TestDispatchDoesNotBlockOnSlowHandler(t *testing.T) {
	cfg, _ := json.Marshal(Config{BrokerURL: "mqtt://127.0.0.1:1"})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err, "the driver must build")
	driver, ok := d.(*Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")

	release := make(chan struct{})
	defer close(release)
	_, err = driver.Subscribe("slow/topic", 0, func(string, []byte) { <-release })
	require.NoError(t, err, "the wedged handler must be subscribed before dispatch is exercised")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go driver.dispatchLoop(ctx)

	// Far more messages than the queue holds, with the only handler wedged.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < deliveryQueueSize*2; i++ {
			driver.dispatch(Message{Topic: "slow/topic", Payload: []byte("payload")})
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t,
			"dispatch blocked behind a slow handler, which would stall paho's reader and kill the keepalive")
	}

	driver.mu.Lock()
	dropped := driver.dropped
	driver.mu.Unlock()
	assert.NotZero(t, dropped,
		"a saturated queue must shed load and say so, since the alternative is blocking the connection itself")
}

// TestDispatchCopiesPayload covers ownership of the payload bytes. Dispatch
// queues the message and returns, so the handler runs after paho has moved on
// and may already have reused the packet buffer — a queued message that
// referenced it would deliver whatever arrived next. The test overwrites the
// caller's buffer before the handler ever runs, which is exactly what paho does
// under load and never does when the broker is quiet.
func TestDispatchCopiesPayload(t *testing.T) {
	cfg, _ := json.Marshal(Config{BrokerURL: "mqtt://127.0.0.1:1"})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err, "the driver must build")
	driver, ok := d.(*Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")

	got := make(chan string, 1)
	_, err = driver.Subscribe("copy/topic", 0, func(_ string, payload []byte) { got <- string(payload) })
	require.NoError(t, err, "the handler must be subscribed before dispatch is exercised")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	payload := []byte("original")
	driver.dispatch(Message{Topic: "copy/topic", Payload: payload})
	// Reuse the caller's buffer before the handler ever runs.
	copy(payload, []byte("OVERWRIT"))
	go driver.dispatchLoop(ctx)

	select {
	case delivered := <-got:
		assert.Equal(t, "original", delivered,
			"the queued message must own its bytes, or a busy broker delivers whatever arrived after them")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the handler never ran")
	}
}

// TestDriverReportsConnectionLoss pins a regression that mattered more than any
// other in the field: a dead session kept reporting itself connected, so every
// MQTT trigger sat deaf while the connections page and the metrics both showed
// green. Killing the broker under a live driver is the only way to produce
// that, and the status has to move on its own.
func TestDriverReportsConnectionLoss(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	stopBroker(t, server)
	waitFor(t, 15*time.Second, func() bool {
		return driver.Status().State == integrations.StateError
	}, "the status stayed connected after the broker died, which is the failure that leaves flows silently deaf")
}

// TestDriverResubscribesAfterReconnect covers the half of reconnection that is
// easy to miss. Reconnecting the session is visible and gets noticed; restoring
// the filters on it is not, and a driver that reconnects without resubscribing
// reports itself perfectly healthy while receiving nothing ever again. The
// client is dropped from the broker side, as a broker restart does.
func TestDriverResubscribesAfterReconnect(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	var mu sync.Mutex
	var received int
	_, err := driver.Subscribe("sensor/state", 0, func(string, []byte) {
		mu.Lock()
		received++
		mu.Unlock()
	})
	require.NoError(t, err, "the filter must be subscribed before the reconnection means anything")

	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("sensor/state", []byte("1"), false, 0)
		mu.Lock()
		defer mu.Unlock()
		return received > 0
	}, "the subscription never worked in the first place")

	// Drop the client from the broker side and wait for the session to return.
	for _, client := range server.Clients.GetAll() {
		if client.ID != "inline" {
			_ = server.DisconnectClient(client, packets.ErrServerShuttingDown)
		}
	}
	waitFor(t, 15*time.Second, func() bool {
		driver.mu.Lock()
		reconnects := driver.reconnects
		driver.mu.Unlock()
		return driver.Status().State == integrations.StateConnected && reconnects > 1
	}, "the driver never reconnected after the broker dropped it")

	mu.Lock()
	before := received
	mu.Unlock()
	waitFor(t, 15*time.Second, func() bool {
		_ = server.Publish("sensor/state", []byte("2"), false, 0)
		mu.Lock()
		defer mu.Unlock()
		return received > before
	}, "the session came back but the filter did not: the driver reports healthy and receives nothing")
}

// TestDriverRetainedAndGrantedQoS covers what a handler learns beyond the
// payload: a message the broker hands over from its retained store when the
// subscription is made arrives marked retained, a message published while
// subscribed arrives live, and the QoS the broker granted is recorded per
// filter.
func TestDriverRetainedAndGrantedQoS(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	require.NoError(t, server.Publish("dev/state", []byte("before"), true, 0), "a retained message is waiting")
	got := make(chan Message, 4)
	unsub, err := driver.SubscribeMessages("dev/state", 1, func(m Message) { got <- m })
	require.NoError(t, err)
	defer unsub()

	next := func() Message {
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			require.FailNow(t, "no message arrived")
			return Message{}
		}
	}
	first := next()
	assert.Equal(t, "before", string(first.Payload))
	assert.True(t, first.Retained, "a message from the retained store says so")

	waitFor(t, 2*time.Second, func() bool { _, ok := driver.GrantedQoS("dev/state"); return ok },
		"the granted QoS was never recorded")
	granted, _ := driver.GrantedQoS("dev/state")
	assert.Equal(t, byte(1), granted)

	require.NoError(t, server.Publish("dev/state", []byte("live"), true, 0))
	live := next()
	assert.Equal(t, "live", string(live.Payload))
	assert.False(t, live.Retained, "a message published while subscribed is live, even when published retained")
}

// TestPublishRefusesIllFormedTopic locks that a publish topic the broker
// must treat as a malformed packet is refused before it is sent. Topics are
// templated from messages, and one holding bytes that are not UTF-8 made the
// broker close the session, which took every subscription on the connection
// down with it until the reconnect.
func TestPublishRefusesIllFormedTopic(t *testing.T) {
	_, addr := startBroker(t)
	var statuses []integrations.Status
	driver := startDriver(t, addr, &statuses)

	err := driver.Publish(context.Background(), "sensors/\xff/state", 0, false, []byte("1"))
	assert.Error(t, err, "a topic that is not UTF-8 must be refused, not sent")

	// A broker that received the packet drops the session within moments,
	// and the driver connects again.
	time.Sleep(500 * time.Millisecond)
	driver.mu.Lock()
	connects := driver.reconnects
	driver.mu.Unlock()
	assert.Equal(t, 1, connects, "the session must not be dropped and reestablished over a refused publish")
}

// TestDriverReplaysRetainedToLateSubscriber locks that a handler joining a
// filter the session already holds still receives the broker's retained
// message, and that the handler already there is not handed it again. The
// driver sent no SUBSCRIBE for a filter it held, so the broker replayed
// nothing: Zigbee2MQTT discovery timed out once the device list sync held the
// same topic, and a second device on a shared state topic stayed empty until
// the next report.
func TestDriverReplaysRetainedToLateSubscriber(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)
	require.NoError(t, server.Publish("dev/state", []byte("before"), true, 0), "a retained message is waiting")

	first := make(chan Message, 4)
	unsubFirst, err := driver.SubscribeMessages("dev/state", 0, func(m Message) { first <- m })
	require.NoError(t, err)
	defer unsubFirst()
	select {
	case m := <-first:
		require.True(t, m.Retained, "the first handler reads the retained message when it subscribes")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the first handler never received the retained message")
	}

	late := make(chan Message, 4)
	unsubLate, err := driver.SubscribeMessages("dev/state", 0, func(m Message) { late <- m })
	require.NoError(t, err)
	defer unsubLate()
	select {
	case m := <-late:
		assert.Equal(t, "before", string(m.Payload), "the late handler reads the same retained message")
		assert.True(t, m.Retained, "the replay comes from the retained store and says so")
	case <-time.After(3 * time.Second):
		require.FailNow(t, "a handler joining a held filter never received the retained message")
	}

	select {
	case m := <-first:
		assert.Failf(t, "the first handler received the retained message twice",
			"it had already read %q when the late handler joined", m.Payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestDriverKeepsReplayFromHandlerThatReadTopicLive locks the other half of
// the late-subscriber replay: a handler that read a topic's value as it was
// published must not be handed the broker's retained copy of that value when
// another handler joins the filter and the driver sends SUBSCRIBE again. An
// mqtt-in trigger would otherwise fire a second time on a stale value each
// time another flow on the same topic is deployed.
func TestDriverKeepsReplayFromHandlerThatReadTopicLive(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	first := make(chan Message, 4)
	unsubFirst, err := driver.SubscribeMessages("dev/state", 0, func(m Message) { first <- m })
	require.NoError(t, err)
	defer unsubFirst()
	waitFor(t, 5*time.Second, func() bool { return driver.Subscribed("dev/state") }, "the filter was never subscribed")

	// The broker both forwards this publish to the first handler and keeps it
	// as the topic's retained message.
	require.NoError(t, server.Publish("dev/state", []byte("ON"), true, 0))
	select {
	case m := <-first:
		require.Equal(t, "ON", string(m.Payload), "the first handler reads the publish")
		require.False(t, m.Retained, "a message published to a held subscription arrives live")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the first handler never received the live message")
	}

	late := make(chan Message, 4)
	unsubLate, err := driver.SubscribeMessages("dev/state", 0, func(m Message) { late <- m })
	require.NoError(t, err)
	defer unsubLate()
	select {
	case m := <-late:
		assert.Equal(t, "ON", string(m.Payload), "the late handler reads the retained message")
		assert.True(t, m.Retained, "the replay comes from the retained store and says so")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the late handler never received the retained message")
	}

	select {
	case m := <-first:
		assert.Failf(t, "the first handler was handed a value it had already read",
			"%q arrived again with retained=%v when the late handler joined", m.Payload, m.Retained)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestSubscribedFollowsTheSession locks that Subscribed answers for the
// current session. It read the record of filters to remove from the broker,
// which outlives a disconnect, so a filter kept reporting live while the
// session that held it was gone and nothing could arrive on it.
func TestSubscribedFollowsTheSession(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr, nil)

	unsub, err := driver.Subscribe("dev/state", 0, func(string, []byte) {})
	require.NoError(t, err)
	defer unsub()
	waitFor(t, 5*time.Second, func() bool { return driver.Subscribed("dev/state") }, "the filter was never subscribed")

	stopBroker(t, server)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateError
	}, "the driver never noticed the broker going away")
	assert.False(t, driver.Subscribed("dev/state"), "a filter is not live on a session that dropped")
}

// TestDroppedMessageKeepsReplayNew pins the replay rule after a full queue. A
// handler that read v1 live and then lost v2 to the full queue does not hold
// what the retained store holds, so the replay another handler's SUBSCRIBE
// triggers must still reach it.
func TestDroppedMessageKeepsReplayNew(t *testing.T) {
	cfg, _ := json.Marshal(Config{BrokerURL: "mqtt://127.0.0.1:1"})
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err, "the driver must build")
	driver := d.(*Driver)
	_, err = driver.Subscribe("dev/#", 0, func(string, []byte) {})
	require.NoError(t, err)

	driver.dispatch(Message{Topic: "dev/x", Payload: []byte("v1")})
	for len(driver.deliveries) < cap(driver.deliveries) {
		driver.deliveries <- delivery{}
	}
	driver.dispatch(Message{Topic: "dev/x", Payload: []byte("v2")})
	for len(driver.deliveries) > 0 {
		<-driver.deliveries
	}

	driver.dispatch(Message{Topic: "dev/x", Payload: []byte("v2"), Retained: true})
	require.Len(t, driver.deliveries, 1, "the replay must be queued for the handler that missed v2")
	assert.Equal(t, "v2", string((<-driver.deliveries).msg.Payload))
}
