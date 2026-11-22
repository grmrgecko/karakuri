package mq

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRevalidateWithdrawsRevokedSubscriptions pins the fix for open sockets
// keeping grants after they were revoked. Subscription gates used to run only
// when subscribing, so removing a share, a role, or a plugin left delivery
// running for as long as the socket stayed open. A recheck must withdraw a
// subscription the gate now refuses, tell the client which topic it lost, and
// leave a still-permitted one alone.
func TestRevalidateWithdrawsRevokedSubscriptions(t *testing.T) {
	allowed := map[string]bool{"room:kitchen": true, "room:hall": true}
	hub := &Hub{clients: map[string]*Client{}, recheck: make(chan struct{}, 1), log: zap.NewNop()}
	hub.RegisterTopicPermissionCallback("room:*", func(_, topic string) bool { return allowed[topic] })

	client := &Client{
		ID: "c1", subs: map[string]string{}, dashboards: map[string]string{},
		send: make(chan *Message, clientQueueDepth),
	}
	hub.subscribe(client, "room:kitchen", "")
	hub.subscribe(client, "room:hall", "")
	require.Len(t, client.subs, 2, "both topics are permitted when subscribed")
	t.Cleanup(func() { hub.removeClient(client) })

	allowed["room:kitchen"] = false
	hub.revalidate(client)

	assert.NotContains(t, client.subs, "room:kitchen", "a topic the gate now refuses must stop being delivered")
	assert.Contains(t, client.subs, "room:hall", "a topic still permitted must keep its subscription")
	require.Len(t, client.send, 1, "the client must be told about the withdrawn topic")
	msg := <-client.send
	assert.Equal(t, "revoked", msg.Type, "the notice names the revocation")
	assert.Equal(t, "room:kitchen", msg.Topic, "the notice names the topic that was withdrawn")
}
