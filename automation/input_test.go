package automation

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pointerPeer represents the OS input boundary; the protocol test must not hold
// buttons on the developer's actual desktop.
type pointerPeer struct {
	mu              sync.Mutex
	down            bool
	pressed         chan struct{}
	clicked         chan bool
	cleanupCanceled bool
}

func (p *pointerPeer) Move(context.Context, int, int, bool) error { return nil }
func (p *pointerPeer) Hold(ctx context.Context, _ Button, down bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
	if down {
		close(p.pressed)
	} else {
		p.cleanupCanceled = ctx.Err() != nil
	}
	return nil
}
func (p *pointerPeer) Click(context.Context, Button, int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clicked <- p.down
	return nil
}
func (*pointerPeer) Scroll(context.Context, int, bool) error    { return nil }
func (*pointerPeer) Position(context.Context) (int, int, error) { return 0, 0, nil }

// TestDragDisconnect pins the real shared-session cancellation boundary: losing
// a drag caller releases the OS button before another client's click can proceed.
func TestDragDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pointer := &pointerPeer{pressed: make(chan struct{}), clicked: make(chan bool, 1)}
	session := &Session{Mouse: pointer, env: Environment{Backends: map[string]string{}}}
	wireInput(session)
	session.Desktop = localDesktop{session}
	server := NewSessionServer(session)
	clients := make([]*Session, 0, 2)
	done := make([]chan error, 0, 2)
	for i := 0; i < 2; i++ {
		clientConn, serverConn := net.Pipe()
		ch := make(chan error, 1)
		done = append(done, ch)
		go func() { ch <- server.Serve(ctx, serverConn) }()
		client, err := NewRemoteSession(ctx, clientConn)
		require.NoError(t, err)
		clients = append(clients, client)
		t.Cleanup(func() { _ = client.Close() })
	}
	dragCtx, stopDrag := context.WithCancel(ctx)
	defer stopDrag()
	dragDone := make(chan error, 1)
	go func() { dragDone <- clients[0].Desktop.Drag(dragCtx, DragRequest{EndX: 200, Duration: time.Second}) }()
	select {
	case <-pointer.pressed:
	case <-ctx.Done():
		t.Fatal("drag did not press its button")
	}
	clickDone := make(chan error, 1)
	go func() { clickDone <- clients[1].Mouse.Click(ctx, "left", 1) }()
	select {
	case <-pointer.clicked:
		t.Fatal("another client clicked during the drag")
	case <-time.After(20 * time.Millisecond):
	}
	stopDrag()
	require.ErrorIs(t, <-dragDone, context.Canceled)
	require.NoError(t, <-clickDone)
	assert.False(t, <-pointer.clicked, "the later click must observe a released OS button")
	pointer.mu.Lock()
	assert.False(t, pointer.cleanupCanceled, "button release must use a fresh bounded context")
	pointer.mu.Unlock()
	require.NoError(t, clients[1].Close())
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("server retained a disconnected client")
		}
	}
}
