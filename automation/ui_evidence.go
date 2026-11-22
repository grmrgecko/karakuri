package automation

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// SaveStateEvidence captures a failed condition within the caller's deadline.
// Files remain on the host until the operator removes them.
func SaveStateEvidence(ctx context.Context, s *Session, w WindowInfo, o StateOptions, root string, cause error) (string, error) {
	if root == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(config, "karakuri-agent", "captures")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(root, "state-")
	if err != nil {
		return "", err
	}
	type snapshotResult struct {
		value WindowSnapshot
		err   error
	}
	type imageResult struct {
		value image.Image
		err   error
	}
	snapshots := make(chan snapshotResult, 1)
	images := make(chan imageResult, 1)
	go func() {
		value, err := ReadWindowSnapshot(ctx, s, w, true, false)
		snapshots <- snapshotResult{value, err}
	}()
	go func() {
		value, err := CaptureStateRegion(ctx, s, w, image.Rectangle{})
		images <- imageResult{value, err}
	}()
	snapshot := NewWindowSnapshot(s, w)
	var img image.Image
	var snapshotErr, screenErr error
	for snapshots != nil || images != nil {
		select {
		case r := <-snapshots:
			snapshot, snapshotErr = r.value, r.err
			snapshots = nil
		case r := <-images:
			img, screenErr = r.value, r.err
			images = nil
		case <-ctx.Done():
			if snapshots != nil {
				snapshotErr = ctx.Err()
				snapshots = nil
			}
			if images != nil {
				screenErr = ctx.Err()
				images = nil
			}
		}
	}
	if snapshotErr != nil {
		snapshot.InspectionError = snapshotErr.Error()
	}
	if screenErr == nil && img != nil {
		f, err := os.OpenFile(filepath.Join(directory, "screenshot.png"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			err = png.Encode(f, img)
			err = errors.Join(err, f.Close())
		}
		screenErr = err
	}
	report := struct {
		Error           string         `json:"error"`
		Condition       StateOptions   `json:"condition"`
		Snapshot        WindowSnapshot `json:"snapshot"`
		ScreenshotError string         `json:"screenshot_error,omitempty"`
	}{Error: cause.Error(), Condition: o, Snapshot: snapshot}
	if screenErr != nil {
		report.ScreenshotError = screenErr.Error()
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(directory, "snapshot.json"), append(data, '\n'), 0600)
	}
	return directory, errors.Join(err, snapshotErr, screenErr)
}
