package automation

import (
	"context"
	"image"
	"image/color"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// TestHelperRoundTrip drives the real helper protocol end to end over a pair of
// pipes: the client stubs a privileged Session uses on one side, serveSession on
// the other. It locks the three things the crossing has to preserve and that
// nothing else checks — a value's contents, an image's pixels, and an error's
// kind — because a privileged agent has no second route to any capability if
// the wire loses them.
func TestHelperRoundTrip(t *testing.T) {
	client := startTestHelper(t, &Session{
		Clipboard: fakeClipboard{text: "helper-roundtrip"},
		Screen:    fakeScreen{width: 7, height: 3, mapping: &autoapi.CaptureMapping{OriginX: -120, OriginY: 80, UnitsPerPixelX: 0.5, UnitsPerPixelY: 1}},
		Notifier:  fakeNotifier{},
		Media:     fakeCapability{err: autoapi.MissingTool("control playback", "playerctl")},
		Mouse:     fakeMouse{err: autoapi.ErrUnsupported},
	})
	ctx := context.Background()

	text, err := helperClipboard{client}.Read(ctx, autoapi.SelectionClipboard)
	require.NoError(t, err)
	assert.Equal(t, "helper-roundtrip", text, "the clipboard's contents must survive the crossing unchanged")

	img, err := helperScreen{client}.Capture(ctx, autoapi.CaptureRequest{})
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 7, 3), img.Bounds(), "a screenshot must arrive at the size it was taken")
	assert.Equal(t, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff}, img.At(1, 1),
		"a screenshot's pixels must survive the PNG the wire carries it as")

	assert.Equal(t, &autoapi.CaptureMapping{OriginX: -120, OriginY: 80, UnitsPerPixelX: 0.5, UnitsPerPixelY: 1}, autoapi.ImageCaptureMapping(img), "capture provenance must survive PNG normalization")

	// A caller with a second route to try checks the kind before giving up, so
	// the kind is what has to cross rather than only the message.
	err = helperMedia{client}.Control(ctx, autoapi.MediaRequest{Action: autoapi.MediaPlay})
	require.Error(t, err)
	assert.True(t, autoapi.IsMissingTool(err), "a missing tool must still read as one after crossing the wire")
	assert.Contains(t, err.Error(), "playerctl", "the missing tool's name is what makes the message useful")

	_, _, err = helperMouse{client}.Position(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, autoapi.ErrUnsupported,
		"a capability the session cannot offer must still read as unsupported after crossing the wire")
}

// TestHelperCallsAreMultiplexed locks the reason replies are correlated by id
// rather than answered in order: a dialog waits for a person, and on a screen
// nobody is sitting at it waits for its whole timeout. Serializing the pipe
// would make every other node on the agent wait with it.
func TestHelperCallsAreMultiplexed(t *testing.T) {
	release := make(chan struct{})
	client := startTestHelper(t, &Session{
		Dialog:   &blockingDialog{release: release},
		Notifier: fakeNotifier{},
	})
	ctx := context.Background()

	asked := make(chan error, 1)
	go func() {
		_, err := helperDialog{client}.Ask(ctx, autoapi.DialogRequest{Kind: autoapi.DialogQuestion})
		asked <- err
	}()

	// The notification has to complete while the dialog is still open, which is
	// only true if the two calls are in flight at once.
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, helperNotifier{client}.Notify(deadline, autoapi.Notification{Title: "meanwhile"}),
		"a notification must not wait for a dialog nobody has answered")

	close(release)
	require.NoError(t, <-asked)
}

// startTestHelper runs serveSession against sess over a pipe pair and returns a
// client wired to it, the same one a delegating Session uses. The transport is
// the real protocol; only the session behind it is a fixture, because the point
// is the crossing rather than any one backend.
func startTestHelper(t *testing.T, sess *Session) *helper {
	t.Helper()

	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = serveSession(context.Background(), sess, toServer, fromServer)
	}()
	t.Cleanup(func() {
		_ = fromClient.Close()
		_ = toClient.Close()
		<-served
	})

	return newHelper(func(context.Context) (*hrpc.Conn, func(), error) {
		return hrpc.NewConn(toClient, fromClient), func() {}, nil
	}, nil)
}

// The fixtures below stand in for the platform backends, which cannot run in a
// test: each one is the smallest capability that still exercises a distinct
// shape on the wire.

type fakeClipboard struct{ text string }

func (f fakeClipboard) Read(context.Context, autoapi.Selection) (string, error) { return f.text, nil }
func (f fakeClipboard) Write(context.Context, autoapi.Selection, string) error  { return nil }

type fakeScreen struct {
	width, height int
	mapping       *autoapi.CaptureMapping
}

// Capture returns an image whose pixels are all one non-trivial colour, so a
// wire that dropped the image data cannot pass by returning a blank one.
func (f fakeScreen) Capture(context.Context, autoapi.CaptureRequest) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, f.width, f.height))
	for x := range f.width {
		for y := range f.height {
			img.Set(x, y, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff})
		}
	}
	return autoapi.WithCaptureMapping(img, f.mapping), nil
}

type fakeNotifier struct{}

func (fakeNotifier) Notify(context.Context, autoapi.Notification) error { return nil }

// fakeCapability answers every method it implements with one canned error, for
// the cases where the failure is the thing under test.
type fakeCapability struct{ err error }

func (f fakeCapability) Control(context.Context, autoapi.MediaRequest) error { return f.err }

type fakeMouse struct{ err error }

func (f fakeMouse) Move(context.Context, int, int, bool) error       { return f.err }
func (f fakeMouse) Click(context.Context, autoapi.Button, int) error { return f.err }
func (f fakeMouse) Hold(context.Context, autoapi.Button, bool) error { return f.err }
func (f fakeMouse) Scroll(context.Context, int, bool) error          { return f.err }
func (f fakeMouse) Position(context.Context) (int, int, error)       { return 0, 0, f.err }

// blockingDialog holds its answer until the test releases it, standing in for a
// dialog waiting on a person.
type blockingDialog struct{ release chan struct{} }

func (d *blockingDialog) Ask(ctx context.Context, _ autoapi.DialogRequest) (autoapi.DialogResult, error) {
	select {
	case <-d.release:
	case <-ctx.Done():
		return autoapi.DialogResult{}, ctx.Err()
	}
	return autoapi.DialogResult{Value: "ok", Answered: true}, nil
}
