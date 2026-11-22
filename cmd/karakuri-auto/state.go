package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/grmrgecko/karakuri/automation"
)

type stateOptions struct {
	window, text, role, evidence string
	enabled, gone                bool
	timeout, stable              time.Duration
	region                       image.Rectangle
}

func parseStateOptions(args []string) (stateOptions, error) {
	o := stateOptions{timeout: 30 * time.Second}
	if len(args) == 0 || args[0] != "state" {
		return o, fmt.Errorf("usage: wait|assert state --window <match> [--text <substring>] [--role <role>] [--enabled] [--gone] [--stable seconds] [--region x,y,width,height] [--timeout seconds] [--evidence directory]")
	}
	seen := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		option := args[i]
		if seen[option] {
			return o, fmt.Errorf("duplicate state option %q", option)
		}
		seen[option] = true
		switch option {
		case "--enabled":
			o.enabled = true
			continue
		case "--gone":
			o.gone = true
			continue
		case "--window", "--text", "--role", "--stable", "--region", "--timeout", "--evidence":
		default:
			return o, fmt.Errorf("unknown state option %q", option)
		}
		i++
		if i == len(args) || args[i] == "" {
			return o, fmt.Errorf("%s needs a value", option)
		}
		value := args[i]
		switch option {
		case "--window":
			o.window = value
		case "--text":
			o.text = value
		case "--role":
			o.role = value
		case "--evidence":
			o.evidence = value
		case "--timeout", "--stable":
			duration, err := seconds(value)
			if err != nil {
				return o, err
			}
			if duration <= 0 {
				return o, fmt.Errorf("%s must be positive", option)
			}
			if option == "--timeout" {
				o.timeout = duration
			} else {
				o.stable = duration
			}
		case "--region":
			parsed, err := parseScreenshotOptions([]string{"--region", value})
			if err != nil {
				return o, err
			}
			r := parsed.request
			if r.X < 0 || r.Y < 0 {
				return o, fmt.Errorf("state region coordinates must be relative to the window and nonnegative")
			}
			o.region = image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height)
		}
	}
	if o.window == "" {
		return o, fmt.Errorf("state requires --window")
	}
	if o.stable > 0 {
		if o.text != "" || o.role != "" || o.enabled || o.gone {
			return o, fmt.Errorf("--stable cannot be combined with element conditions")
		}
		if o.stable >= o.timeout {
			return o, fmt.Errorf("--timeout must exceed --stable")
		}
	} else {
		if !o.region.Empty() {
			return o, fmt.Errorf("--region requires --stable")
		}
		if o.text == "" && o.role == "" && !o.enabled {
			return o, fmt.Errorf("state requires --text, --role, --enabled, or --stable")
		}
	}
	return o, nil
}

type windowSnapshot = automation.WindowSnapshot

func readWindowSnapshot(ctx context.Context, sess *automation.Session, w automation.WindowInfo, all, enabled bool) (windowSnapshot, error) {
	return automation.ReadWindowSnapshot(ctx, sess, w, all, enabled)
}
func newWindowSnapshot(sess *automation.Session, w automation.WindowInfo) windowSnapshot {
	return automation.NewWindowSnapshot(sess, w)
}
func (o stateOptions) shared() automation.StateOptions {
	return automation.StateOptions{Text: o.text, Role: o.role, Enabled: o.enabled, Gone: o.gone, Stable: o.stable, Region: o.region}
}

func snapshotCommand(ctx context.Context, sess *automation.Session, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("usage: snapshot <window> [--all] [--enabled]")
	}
	all, enabled := false, false
	for _, option := range args[1:] {
		switch option {
		case "--all":
			all = true
		case "--enabled":
			enabled = true
		default:
			return fmt.Errorf("unknown snapshot option %q", option)
		}
	}
	w, err := selectedWindow(ctx, sess, args[0])
	if err != nil {
		return err
	}
	snapshot, err := readWindowSnapshot(ctx, sess, w, all, enabled)
	if writeErr := printJSON(snapshot); writeErr != nil {
		return writeErr
	}
	return err
}

func captureStateRegion(ctx context.Context, sess *automation.Session, w automation.WindowInfo, r image.Rectangle) (image.Image, error) {
	return automation.CaptureStateRegion(ctx, sess, w, r)
}
func stateImageDigest(img image.Image) [32]byte { return automation.StateImageDigest(img) }

func waitState(ctx context.Context, sess *automation.Session, args []string) error {
	o, err := parseStateOptions(args)
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	w, err := selectedWindow(waitCtx, sess, o.window)
	if err != nil {
		return fmt.Errorf("selecting state window: %w", err)
	}
	// Pin the ID so a replacement window with the same title cannot satisfy a wait.
	err = pollWindowState(waitCtx, sess, &w, o)
	if err != nil {
		if waitCtx.Err() != nil && !errors.Is(err, waitCtx.Err()) {
			err = waitCtx.Err()
		}
		directory, captureErr := saveStateEvidence(sess, w, o, err)
		if captureErr != nil {
			return fmt.Errorf("waiting for state: %w; evidence %q: %v", err, directory, captureErr)
		}
		return fmt.Errorf("waiting for state: %w; evidence saved to %s", err, directory)
	}
	return printJSON(map[string]any{"window_id": w.ID, "matched": true, "gone": o.gone})
}

func pollWindowState(ctx context.Context, sess *automation.Session, w *automation.WindowInfo, o stateOptions) error {
	return automation.PollWindowState(ctx, sess, w, o.shared())
}

// saveStateEvidence uses fresh bounded contexts because the state deadline has
// usually expired. Separate connections keep a canceled inspection request
// from closing the resident connection needed for the screenshot.
func saveStateEvidence(sess *automation.Session, w automation.WindowInfo, o stateOptions, cause error) (string, error) {
	root := o.evidence
	if root == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(config, "karakuri-auto", "captures")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(root, "state-")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	snapshot := newWindowSnapshot(sess, w)
	inspectionSession, snapshotErr := commandSession(ctx, automationOptions())
	if snapshotErr == nil {
		snapshot, snapshotErr = readWindowSnapshot(ctx, inspectionSession, w, true, false)
		_ = inspectionSession.Close()
	} else {
		snapshot.InspectionError = snapshotErr.Error()
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	screenSession, screenErr := commandSession(ctx, automationOptions())
	var img image.Image
	if screenErr == nil {
		img, screenErr = captureStateRegion(ctx, screenSession, w, image.Rectangle{})
		_ = screenSession.Close()
	}
	cancel()
	if screenErr == nil {
		screenErr = writeStatePNG(filepath.Join(directory, "screenshot.png"), img)
	}
	report := struct {
		Error           string         `json:"error"`
		Condition       map[string]any `json:"condition"`
		ScreenshotError string         `json:"screenshot_error,omitempty"`
		Snapshot        windowSnapshot `json:"snapshot"`
	}{Error: cause.Error(), Snapshot: snapshot, Condition: map[string]any{
		"window": o.window, "text": o.text, "role": o.role, "enabled": o.enabled,
		"gone": o.gone, "stable_seconds": o.stable.Seconds(),
		"region": map[string]int{"x": o.region.Min.X, "y": o.region.Min.Y, "width": o.region.Dx(), "height": o.region.Dy()},
	}}
	if screenErr != nil {
		report.ScreenshotError = screenErr.Error()
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(directory, "snapshot.json"), append(data, '\n'), 0600)
	}
	if err != nil {
		return directory, err
	}
	if snapshotErr != nil || screenErr != nil {
		return directory, fmt.Errorf("partial capture (inspection: %v; screenshot: %v)", snapshotErr, screenErr)
	}
	return directory, nil
}

func writeStatePNG(path string, img image.Image) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = png.Encode(f, img)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
