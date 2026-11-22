package frigate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"go.uber.org/zap"
)

// Stream limits.
const (
	maxFrameBytes = 8 << 20
	// subscriberBuffer is how many frames a viewer may fall behind before
	// frames are dropped for it rather than holding up the others.
	subscriberBuffer = 2
)

// streamLinger is how long an upstream stays open after its last viewer
// leaves, so a dashboard reload does not restart Frigate's encoder. It is a
// variable so tests can shorten it.
var streamLinger = 10 * time.Second

// streamKey names one shared upstream.
type streamKey struct {
	camera string
	height int
	fps    int
	bbox   bool
}

// upstream is one MJPEG stream from Frigate shared by its viewers.
type upstream struct {
	key    streamKey
	cancel context.CancelFunc
	subs   map[int]chan []byte
	linger *time.Timer
	done   bool
}

// streamHub keeps one upstream per camera and shape.
type streamHub struct {
	open func(ctx context.Context, key streamKey) (io.ReadCloser, string, error)
	log  *zap.Logger

	mu      sync.Mutex
	next    int
	streams map[streamKey]*upstream
}

func newStreamHub(open func(ctx context.Context, key streamKey) (io.ReadCloser, string, error), log *zap.Logger) *streamHub {
	return &streamHub{open: open, log: log, streams: map[streamKey]*upstream{}}
}

// Stream subscribes to a camera's shared live stream.
func (d *Driver) Stream(ctx context.Context, cameraName string, opts nvr.ImageOptions) (*nvr.Subscription, error) {
	if !CameraNameRe.MatchString(cameraName) {
		return nil, fmt.Errorf("invalid camera name %q", cameraName)
	}
	if err := d.ready(cameraName); err != nil {
		return nil, err
	}
	fps := opts.FPS
	if fps <= 0 || fps > 10 {
		fps = 5
	}
	height := opts.Height
	if height < 0 || height > 2160 {
		height = 0
	}
	return d.streams.subscribe(streamKey{camera: cameraName, height: height, fps: fps, bbox: opts.BBox})
}

// openStream requests Frigate's MJPEG stream for one shape.
func (d *Driver) openStream(ctx context.Context, key streamKey) (io.ReadCloser, string, error) {
	v := url.Values{}
	v.Set("fps", strconv.Itoa(key.fps))
	if key.height > 0 {
		v.Set("height", strconv.Itoa(key.height))
	}
	if key.bbox {
		v.Set("bbox", "1")
	}
	resp, err := d.fetch(ctx, "/api/"+key.camera+"?"+v.Encode(), nil, 0, 0)
	if err != nil {
		return nil, "", err
	}
	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		resp.Body.Close()
		return nil, "", errors.New("frigate did not answer with an MJPEG stream")
	}
	return resp.Body, params["boundary"], nil
}

// subscribe joins or starts the upstream for a key.
func (h *streamHub) subscribe(key streamKey) (*nvr.Subscription, error) {
	h.mu.Lock()
	up, ok := h.streams[key]
	if !ok || up.done {
		ctx, cancel := context.WithCancel(context.Background())
		body, boundary, err := h.open(ctx, key)
		if err != nil {
			h.mu.Unlock()
			cancel()
			return nil, err
		}
		up = &upstream{key: key, cancel: cancel, subs: map[int]chan []byte{}}
		h.streams[key] = up
		go h.pump(up, body, boundary)
		h.log.Debug("frigate stream opened", zap.String("camera", key.camera), zap.Int("height", key.height))
	}
	if up.linger != nil {
		up.linger.Stop()
		up.linger = nil
	}
	id := h.next
	h.next++
	ch := make(chan []byte, subscriberBuffer)
	up.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	return &nvr.Subscription{Frames: ch, Close: func() {
		once.Do(func() { h.leave(up, id) })
	}}, nil
}

// leave removes a viewer and schedules the upstream's close when it was the
// last one.
func (h *streamHub) leave(up *upstream, id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := up.subs[id]
	if !ok {
		return
	}
	delete(up.subs, id)
	close(ch)
	if len(up.subs) == 0 && !up.done && up.linger == nil {
		up.linger = time.AfterFunc(streamLinger, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if len(up.subs) == 0 && up.linger != nil {
				h.end(up)
			}
		})
	}
}

// end closes an upstream and every viewer still on it. h.mu is held.
func (h *streamHub) end(up *upstream) {
	if up.done {
		return
	}
	up.done = true
	up.cancel()
	for id, ch := range up.subs {
		close(ch)
		delete(up.subs, id)
	}
	if h.streams[up.key] == up {
		delete(h.streams, up.key)
	}
	h.log.Debug("frigate stream closed", zap.String("camera", up.key.camera))
}

// closeAll ends every upstream, for a driver stopping.
func (h *streamHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, up := range h.streams {
		h.end(up)
	}
}

// pump reads frames from an upstream and hands each one to every viewer.
func (h *streamHub) pump(up *upstream, body io.ReadCloser, boundary string) {
	defer body.Close()
	err := readFrames(body, boundary, func(frame []byte) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, ch := range up.subs {
			select {
			case ch <- frame:
			default:
				// A slow viewer misses this frame; the next whole one
				// follows.
			}
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		h.log.Debug("frigate stream ended", zap.String("camera", up.key.camera), zap.Error(err))
	}
	h.mu.Lock()
	h.end(up)
	h.mu.Unlock()
}

// readFrames splits a multipart/x-mixed-replace body into its parts.
func readFrames(body io.Reader, boundary string, onFrame func([]byte)) error {
	mr := multipart.NewReader(bufio.NewReaderSize(body, 64<<10), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var buf bytes.Buffer
		n, err := io.Copy(&buf, io.LimitReader(part, maxFrameBytes+1))
		if err != nil {
			return err
		}
		if n > maxFrameBytes {
			return fmt.Errorf("a frame exceeded %d bytes", maxFrameBytes)
		}
		if n > 0 {
			onFrame(buf.Bytes())
		}
	}
}

// WriteFrame writes one frame as a multipart part, the way a relay hands
// frames on to a viewer.
func WriteFrame(w io.Writer, boundary string, frame []byte) error {
	_, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
	if err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\r\n")
	return err
}

// SetStreamLingerForTest shortens how long an idle upstream stays open and
// returns the function that restores it.
func SetStreamLingerForTest(d time.Duration) (restore func()) {
	old := streamLinger
	streamLinger = d
	return func() { streamLinger = old }
}
