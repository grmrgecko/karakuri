//go:build darwin

package macos

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png" // decode the PNG screencapture writes
	"os"
	"strconv"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// The screen is read out of CoreGraphics directly (capture.go), needing only
// the screen-recording permission the system asks for on first use — the same
// one screencapture needs, which stays behind it for the pixel formats the
// native reader declines to guess at.

// captureScreen photographs the screen natively, with screencapture behind it.
func captureScreen(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	if err := requireScreenCaptureAccess(); err != nil {
		return nil, err
	}
	var err error
	if !req.DesktopCoordinates {
		var img image.Image
		img, err = nativeCaptureScreen(req)
		if err == nil {
			return img, nil
		}
	} else {
		err = fmt.Errorf("desktop-coordinate capture needs screencapture")
	}
	tool, ok := toolsh.ToolPath("screencapture")
	if !ok {
		return nil, err
	}

	file, err := os.CreateTemp("", "karakuri-capture-*.png")
	if err != nil {
		return nil, fmt.Errorf("cannot create a file for the capture: %w", err)
	}
	path := file.Name()
	_ = file.Close()
	defer func() { _ = os.Remove(path) }()

	// -x is no shutter sound, -t names the format, and -D numbers the display
	// from one rather than from zero.
	args := []string{"-x", "-t", "png"}
	if req.Region {
		args = append(args, "-R", fmt.Sprintf("%d,%d,%d,%d", req.X, req.Y, req.Width, req.Height))
	} else {
		args = append(args, "-D", strconv.Itoa(req.Display+1))
	}
	if _, err := toolsh.RunTool(ctx, tool, append(args, path)...); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("screencapture wrote nothing: %w", err)
	}
	return decodeCapture(data)
}

// decodeCapture reads back the PNG screencapture wrote.
func decodeCapture(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("screencapture did not return a readable image: %w", err)
	}
	return img, nil
}
