//go:build darwin

package automation

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strconv"
)

func captureSelectedWindow(ctx context.Context, w WindowInfo) (image.Image, error) {
	id, err := strconv.ParseUint(w.CaptureID, 10, 32)
	if err != nil || id == 0 {
		return nil, fmt.Errorf("window has no CoreGraphics capture id")
	}
	file, err := os.CreateTemp("", "karakuri-window-*.png")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)
	if data, err := exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-o", "-l", strconv.FormatUint(id, 10), path).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("window capture: %w: %s", err, data)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}
