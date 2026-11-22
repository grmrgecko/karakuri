package main

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation"
)

type screenshotOptions struct {
	path    string
	request automation.CaptureRequest
	window  string
	active  bool
}

// parseScreenshotOptions accepts options before or after the output path.
func parseScreenshotOptions(args []string) (screenshotOptions, error) {
	opts := screenshotOptions{path: "karakuri-auto.png"}
	var pathSet, displaySet, windowSet bool
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		switch name {
		case "--region", "--window", "--display":
			if !inline {
				i++
				if i == len(args) {
					return opts, fmt.Errorf("%s needs a value", name)
				}
				value = args[i]
			}
			switch name {
			case "--region":
				if opts.request.Region {
					return opts, fmt.Errorf("--region can only be specified once")
				}
				parts := strings.Split(value, ",")
				if len(parts) != 4 {
					return opts, fmt.Errorf("--region needs x,y,width,height")
				}
				numbers := make([]int, 4)
				for j, part := range parts {
					n, err := strconv.Atoi(strings.TrimSpace(part))
					if err != nil {
						return opts, fmt.Errorf("invalid region coordinate %q", part)
					}
					numbers[j] = n
				}
				if numbers[2] <= 0 || numbers[3] <= 0 {
					return opts, fmt.Errorf("region width and height must be positive")
				}
				maxInt := int(^uint(0) >> 1)
				if numbers[0] > maxInt-numbers[2] || numbers[1] > maxInt-numbers[3] {
					return opts, fmt.Errorf("region coordinates overflow")
				}
				opts.request.Region = true
				opts.request.X, opts.request.Y, opts.request.Width, opts.request.Height = numbers[0], numbers[1], numbers[2], numbers[3]
			case "--window":
				if windowSet || value == "" {
					return opts, fmt.Errorf("--window needs one nonempty match")
				}
				opts.window, windowSet = value, true
			case "--display":
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 || displaySet {
					return opts, fmt.Errorf("--display needs one nonnegative screen index")
				}
				opts.request.Display, displaySet = n, true
			}
		case "--active-window":
			if inline || opts.active {
				return opts, fmt.Errorf("--active-window takes no value and can only be specified once")
			}
			opts.active = true
		case "--":
			if pathSet || i+2 != len(args) {
				return opts, fmt.Errorf("expected one output path after --")
			}
			opts.path, pathSet = args[i+1], true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown screenshot option %q", args[i])
			}
			if pathSet {
				return opts, fmt.Errorf("screenshot accepts one output path")
			}
			opts.path, pathSet = args[i], true
		}
	}
	if opts.active && windowSet || opts.request.Region && (opts.active || windowSet) {
		return opts, fmt.Errorf("choose one of --region, --window, or --active-window")
	}
	if displaySet && (opts.active || windowSet) {
		return opts, fmt.Errorf("--display cannot be combined with a window capture")
	}
	if opts.path == "" {
		return opts, fmt.Errorf("output path cannot be empty")
	}
	return opts, nil
}

// screenshotWindow prefers an exact ID and rejects ambiguous text matches.
func screenshotWindow(windows []automation.WindowInfo, match string, active bool) (automation.WindowInfo, error) {
	if !active {
		for _, w := range windows {
			if w.ID == match {
				return w, nil
			}
		}
	}
	var selected automation.WindowInfo
	count := 0
	for _, w := range windows {
		if active && w.Active || !active && (strings.Contains(w.ID, match) || strings.Contains(w.Title, match) || strings.Contains(w.Class, match)) {
			selected = w
			count++
		}
	}
	if count == 0 {
		return selected, fmt.Errorf("no matching window; run karakuri-auto windows to list windows")
	}
	if count > 1 {
		return selected, fmt.Errorf("%d windows match; use an exact window ID from karakuri-auto windows", count)
	}
	return selected, nil
}

func takeScreenshot(ctx context.Context, sess *automation.Session, args []string) error {
	opts, err := parseScreenshotOptions(args)
	if err != nil {
		return err
	}
	var captured image.Image
	if opts.active || opts.window != "" {
		windows, err := sess.Window.List(ctx)
		if err != nil {
			return err
		}
		w, err := screenshotWindow(windows, opts.window, opts.active)
		if err != nil {
			return err
		}
		if w.Width <= 0 || w.Height <= 0 {
			return fmt.Errorf("window %q has no usable geometry", w.Title)
		}
		captured, err = sess.Desktop.CaptureWindow(ctx, w)
		if err != nil {
			return err
		}
		opts.request.Region, opts.request.DesktopCoordinates = true, true
		opts.request.X, opts.request.Y, opts.request.Width, opts.request.Height = w.X, w.Y, w.Width, w.Height
	}
	img := captured
	if img == nil {
		img, err = sess.Screen.Capture(ctx, opts.request)
		if err != nil {
			return err
		}
	}
	f, err := os.Create(opts.path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(opts.path)
	return nil
}
