package pluginhost

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// driverPeer is the plugin side of a driver stream: it acks the start,
// reports the driver connected, and holds the stream open like a running
// driver does.
type driverPeer struct {
	pb.UnimplementedKarakuriPluginServer
}

func (driverPeer) StartDriver(_ *pb.StartDriverRequest, stream pb.KarakuriPlugin_StartDriverServer) error {
	if err := stream.Send(&pb.DriverEvent{}); err != nil {
		return err
	}
	connected := &pb.DriverStatus{State: integrations.StateConnected, Since: time.Now().Format(time.RFC3339Nano)}
	if err := stream.Send(&pb.DriverEvent{Status: connected}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func (driverPeer) StopDriver(context.Context, *pb.DriverID) (*pb.Empty, error) {
	return &pb.Empty{}, nil
}

// statusLog collects the statuses a driver publishes.
type statusLog struct {
	mu   sync.Mutex
	seen []integrations.Status
}

func (l *statusLog) publish(s integrations.Status) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, s)
}

func (l *statusLog) states() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.seen))
	for _, s := range l.seen {
		out = append(out, s.State)
	}
	return out
}

// startDriverPeer serves a plugin peer on a loopback socket and returns a
// proxy driver connected to it, with the server to stop.
func startDriverPeer(t *testing.T, log *statusLog) (*remoteDriver, *grpc.Server) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "the peer needs a loopback socket")
	gs := grpc.NewServer()
	pb.RegisterKarakuriPluginServer(gs, driverPeer{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "the host must reach the peer")
	t.Cleanup(func() { _ = conn.Close() })

	lp := &loadedPlugin{manifest: ExternalManifest{Name: "peer"}, rpc: pb.NewKarakuriPluginClient(conn)}
	d := newRemoteDriver(lp, "peer-kind", integrations.ConnModel{ID: uuid.New(), Name: "peer"},
		integrations.Deps{PublishStatus: log.publish})
	require.NoError(t, d.Start(context.Background()), "a driver the plugin acks must start")
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateConnected },
		5*time.Second, 10*time.Millisecond, "the plugin's connected status must reach the proxy")
	return d, gs
}

// TestRemoteDriverReportsALostPlugin pins what a plugin-owned connection
// shows once its plugin process is gone: an error. The proxy only ever
// relayed the statuses the plugin sent, so a plugin that crashed while its
// driver was connected left the connection reading connected for good. A
// driver the host stops itself ends the same stream and must not be reported
// as failed.
func TestRemoteDriverReportsALostPlugin(t *testing.T) {
	t.Run("plugin lost", func(t *testing.T) {
		log := &statusLog{}
		d, gs := startDriverPeer(t, log)

		gs.Stop()

		require.Eventually(t, func() bool { return d.Status().State == integrations.StateError },
			5*time.Second, 10*time.Millisecond, "a driver whose plugin is gone must not keep reading connected")
		assert.NotEmpty(t, d.Status().Error, "the status must say why the connection is down")
		states := log.states()
		assert.Equal(t, integrations.StateError, states[len(states)-1],
			"the loss must be published, since nothing polls a driver for its status")
	})

	t.Run("stopped by the host", func(t *testing.T) {
		log := &statusLog{}
		d, _ := startDriverPeer(t, log)

		d.Stop()

		assert.Never(t, func() bool { return d.Status().State == integrations.StateError },
			300*time.Millisecond, 10*time.Millisecond, "stopping a driver is not a failure of its plugin")
		assert.NotContains(t, log.states(), integrations.StateError, "no error may be published for a stopped driver")
	})
}
