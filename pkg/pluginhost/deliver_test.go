package pluginhost

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/grmrgecko/karakuri/pkg/mq"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// countPeer is a plugin whose handler for the first message is still running
// when the second is published, the way the calendar plugin reads a widget's
// sources from the host before it starts polling.
type countPeer struct {
	pb.KarakuriPluginClient

	hold chan struct{}

	mu   sync.Mutex
	seen []string
}

func (p *countPeer) HandleMessage(_ context.Context, in *pb.MessageEvent, _ ...grpc.CallOption) (*pb.Empty, error) {
	var msg struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(in.Payload, &msg)
	if msg.Count > 0 {
		<-p.hold
	}
	p.mu.Lock()
	p.seen = append(p.seen, string(in.Payload))
	p.mu.Unlock()
	return &pb.Empty{}, nil
}

func (p *countPeer) handled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// TestDeliverKeepsPublishOrder pins that a plugin handles broker messages in
// the order they were published. Plugins start and stop polling on
// subscription counts, so a dashboard that subscribes and leaves publishes a
// count of 1 and then 0. Each message used to go out on its own goroutine:
// the 0 could be handled while the 1 was still in flight, and the plugin was
// left polling a topic nobody watches.
func TestDeliverKeepsPublishOrder(t *testing.T) {
	peer := &countPeer{hold: make(chan struct{})}
	ph := newPluginHost(newHostService(), "peer", zap.NewNop())
	ph.attach(peer)

	subscribed := &mq.Message{Topic: "topic:peer:update:count", Type: "subscription_count", Payload: json.RawMessage(`{"count":1}`)}
	left := &mq.Message{Topic: "topic:peer:update:count", Type: "subscription_count", Payload: json.RawMessage(`{"count":0}`)}
	ph.deliver(subscribed)
	ph.deliver(left)

	assert.Never(t, func() bool { return len(peer.handled()) > 0 }, 200*time.Millisecond, 10*time.Millisecond,
		"the later message must wait for the earlier one to be handled")
	close(peer.hold)
	require.Eventually(t, func() bool { return len(peer.handled()) == 2 }, 5*time.Second, 10*time.Millisecond,
		"both messages must reach the plugin")
	assert.Equal(t, []string{`{"count":1}`, `{"count":0}`}, peer.handled(),
		"the plugin must see the count of 0 last, since that is the state the topic ended in")
}
