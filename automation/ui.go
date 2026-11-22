package automation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"strings"
	"time"
)

// ErrConditionUnmet distinguishes an observed false condition from a provider failure.
var ErrConditionUnmet = errors.New("condition is not met")

// StateOptions describes accessible predicates or a stable visible pixel region.
type StateOptions struct {
	ID           string          `json:"id,omitempty"`
	Text         string          `json:"text,omitempty"`
	Role         string          `json:"role,omitempty"`
	Enabled      bool            `json:"enabled"`
	EnabledState string          `json:"enabled_state,omitempty"`
	Gone         bool            `json:"gone"`
	Stable       time.Duration   `json:"stable"`
	Region       image.Rectangle `json:"region"`
}

// Validate rejects mixed pixel and element conditions before touching the desktop.
func (o StateOptions) Validate(timeout time.Duration) error {
	if o.EnabledState != "" && o.EnabledState != "either" && o.EnabledState != "enabled" && o.EnabledState != "disabled" {
		return fmt.Errorf("unknown enabled state %q", o.EnabledState)
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if o.Stable < 0 {
		return fmt.Errorf("stable interval must be nonnegative")
	}
	if o.Stable > 0 {
		if o.Text != "" || o.Role != "" || o.ID != "" || o.requiresEnabledState() || o.Gone {
			return fmt.Errorf("pixel stability cannot be combined with element conditions")
		}
		if o.Stable >= timeout {
			return fmt.Errorf("timeout must exceed the stable interval")
		}
		if o.Region.Min.X < 0 || o.Region.Min.Y < 0 {
			return fmt.Errorf("region coordinates must be nonnegative")
		}
	} else {
		if !o.Region.Empty() {
			return fmt.Errorf("a region requires pixel stability")
		}
		if o.Text == "" && o.Role == "" && o.ID == "" && !o.requiresEnabledState() {
			return fmt.Errorf("an element condition or stable interval is required")
		}
	}
	return nil
}

// SnapshotWindowInfo retains unknown state and geometry for an observed window.
type SnapshotWindowInfo struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Class         string `json:"class"`
	PID           int    `json:"pid"`
	Image         string `json:"image,omitempty"`
	Enabled       *bool  `json:"enabled"`
	X             int    `json:"x"`
	Y             int    `json:"y"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	GeometryKnown bool   `json:"geometry_known"`
}

// WindowSnapshot contains one accessibility observation and its provider context.
type WindowSnapshot struct {
	CapturedAt       time.Time          `json:"captured_at"`
	Window           SnapshotWindowInfo `json:"window"`
	Backends         map[string]string  `json:"backends"`
	Elements         []Element          `json:"elements"`
	InspectionError  string             `json:"inspection_error,omitempty"`
	Controls         []Control          `json:"controls,omitempty"`
	TruncatedSources []string           `json:"truncated_sources,omitempty"`
}

// ReadWindowSnapshot filters a fresh accessibility observation and suppresses protected text.
func ReadWindowSnapshot(ctx context.Context, sess *Session, w WindowInfo, all, enabled bool) (WindowSnapshot, error) {
	return ReadWindowSnapshotWithOptions(ctx, sess, w, SnapshotOptions{All: all, Enabled: enabled})
}

// SnapshotOptions chooses observation sources and bounds filtered output.
type SnapshotOptions struct {
	Source     string
	All        bool
	Enabled    bool
	Role       string
	Text       string
	MaxResults int
}

// ReadWindowSnapshotWithOptions retains per-source observations and reports output truncation.
func ReadWindowSnapshotWithOptions(ctx context.Context, sess *Session, w WindowInfo, o SnapshotOptions) (WindowSnapshot, error) {
	out := NewWindowSnapshot(sess, w)
	if o.MaxResults < 0 {
		return out, fmt.Errorf("snapshot result limit must be nonnegative")
	}
	if o.Source == "" {
		o.Source = "accessibility"
	}
	if o.Source != "accessibility" && o.Source != "native" && o.Source != "both" {
		return out, fmt.Errorf("unknown snapshot source %q", o.Source)
	}
	var readErrors []error
	if o.Source != "native" {
		elements, err := sess.Inspection.Elements(ctx, w)
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("accessibility: %w", err))
		}
		for _, e := range elements {
			if e.Protected {
				e.Text = ""
			}
			if !snapshotMatches(o, e.Visible, e.Enabled, e.Role, e.Name, e.Text) {
				continue
			}
			if o.MaxResults > 0 && len(out.Elements) == o.MaxResults {
				out.TruncatedSources = append(out.TruncatedSources, "accessibility")
				break
			}
			out.Elements = append(out.Elements, e)
		}
	}
	if o.Source != "accessibility" {
		controls, err := sess.Desktop.Controls(ctx, w)
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("native controls: %w", err))
		}
		out.Controls = make([]Control, 0)
		for _, c := range controls {
			if c.Protected {
				c.Text = ""
			}
			role := c.Role
			if role == "" {
				role = c.Class
			}
			if !snapshotMatches(o, c.Visible, c.Enabled, role, c.Name, c.Text) {
				continue
			}
			if o.MaxResults > 0 && len(out.Controls) == o.MaxResults {
				out.TruncatedSources = append(out.TruncatedSources, "native")
				break
			}
			out.Controls = append(out.Controls, c)
		}
	}
	err := errors.Join(readErrors...)
	if err != nil {
		out.InspectionError = err.Error()
	}
	return out, err
}
func snapshotMatches(o SnapshotOptions, visible, enabled bool, role, name, text string) bool {
	return (o.All || visible) && (!o.Enabled || enabled) && (o.Role == "" || strings.EqualFold(o.Role, role)) &&
		(o.Text == "" || strings.Contains(strings.ToLower(name), strings.ToLower(o.Text)) || strings.Contains(strings.ToLower(text), strings.ToLower(o.Text)))
}

// NewWindowSnapshot records metadata before accessibility inspection.
func NewWindowSnapshot(sess *Session, w WindowInfo) WindowSnapshot {
	return WindowSnapshot{
		CapturedAt: time.Now().UTC(),
		Window:     SnapshotWindowInfo{w.ID, w.Title, w.Class, w.PID, w.Image, w.Enabled, w.X, w.Y, w.Width, w.Height, w.Width > 0 && w.Height > 0},
		Backends:   sess.Describe().Backends,
		Elements:   make([]Element, 0),
	}
}

func stateElementMatches(e Element, o StateOptions) bool {
	return e.Visible && (o.ID == "" || e.ID == o.ID) && o.matchesEnabled(e.Enabled) &&
		(o.Role == "" || strings.EqualFold(e.Role, o.Role)) &&
		(o.Text == "" || strings.Contains(strings.ToLower(e.Name), strings.ToLower(o.Text)) ||
			(!e.Protected && strings.Contains(strings.ToLower(e.Text), strings.ToLower(o.Text))))
}

// CaptureStateRegion samples visible desktop pixels, so it also works through
// the resident's capability connection. Occluding windows affect the result.
func CaptureStateRegion(ctx context.Context, sess *Session, w WindowInfo, region image.Rectangle) (image.Image, error) {
	bounds := image.Rect(0, 0, w.Width, w.Height)
	if bounds.Empty() {
		return nil, fmt.Errorf("window %q has no usable geometry", w.Title)
	}
	if region.Empty() {
		region = bounds
	}
	if !region.In(bounds) {
		return nil, fmt.Errorf("state region extends outside the window")
	}
	// Window coordinates come from the provider; reject overflow before capture.
	maxInt := int(^uint(0) >> 1)
	if w.X > maxInt-region.Max.X || w.Y > maxInt-region.Max.Y {
		return nil, fmt.Errorf("window coordinates overflow")
	}
	return sess.Screen.Capture(ctx, CaptureRequest{
		Region: true, DesktopCoordinates: true, X: w.X + region.Min.X, Y: w.Y + region.Min.Y, Width: region.Dx(), Height: region.Dy(),
	})
}

// StateImageDigest ignores image storage layout and includes dimensions to
// distinguish resized regions containing the same pixel bytes.
func StateImageDigest(img image.Image) [32]byte {
	bounds := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	hash := sha256.New()
	fmt.Fprintf(hash, "%d,%d:", bounds.Dx(), bounds.Dy())
	_, _ = hash.Write(rgba.Pix)
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

// PollWindowState pins a window identity while polling its state until cancellation.
func PollWindowState(ctx context.Context, sess *Session, w *WindowInfo, o StateOptions) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var previous [32]byte
	var stableSince time.Time
	var inspectionErr error
	observedUnmet := false
	for {
		if err := ctx.Err(); err != nil {
			if inspectionErr != nil {
				return errors.Join(err, inspectionErr)
			}
			return err
		}
		windows, err := sess.Window.List(ctx)
		if err != nil {
			if observedUnmet && inspectionErr == nil && ctx.Err() != nil && contextErrorOnly(err, ctx.Err()) {
				return ctx.Err()
			}
			return fmt.Errorf("listing windows: %w", err)
		}
		found := false
		for _, current := range windows {
			if current.ID == w.ID {
				*w = current
				found = true
				break
			}
		}
		if !found {
			if o.Gone {
				return ctx.Err()
			}
			return fmt.Errorf("window %q disappeared", w.ID)
		}
		if o.Stable > 0 {
			img, err := CaptureStateRegion(ctx, sess, *w, o.Region)
			if err != nil {
				if observedUnmet && ctx.Err() != nil && contextErrorOnly(err, ctx.Err()) {
					return ctx.Err()
				}
				return fmt.Errorf("sampling window pixels: %w", err)
			}
			digest := StateImageDigest(img)
			now := time.Now()
			if stableSince.IsZero() || digest != previous {
				stableSince = now
				previous = digest
			}
			observedUnmet = true
			if now.Sub(stableSince) >= o.Stable {
				return ctx.Err()
			}
		} else {
			err := checkElementState(ctx, sess, *w, o)
			if err == nil {
				return ctx.Err()
			}
			if err != ErrConditionUnmet {
				if observedUnmet && inspectionErr == nil && ctx.Err() != nil && contextErrorOnly(err, ctx.Err()) {
					return ctx.Err()
				}
				if !o.Gone {
					return err
				}
				inspectionErr = err
			} else {
				inspectionErr = nil
				observedUnmet = true
			}
		}
		select {
		case <-ctx.Done():
			if inspectionErr != nil {
				return errors.Join(ctx.Err(), inspectionErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (o StateOptions) requiresEnabledState() bool {
	return o.EnabledState == "enabled" || o.EnabledState == "disabled" || (o.EnabledState == "" && o.Enabled)
}
func (o StateOptions) matchesEnabled(enabled bool) bool {
	switch o.EnabledState {
	case "enabled":
		return enabled
	case "disabled":
		return !enabled
	case "either":
		return true
	default:
		return !o.Enabled || enabled
	}
}
func checkElementState(ctx context.Context, sess *Session, w WindowInfo, o StateOptions) error {
	elements, err := sess.Inspection.Elements(ctx, w)
	if err != nil {
		return fmt.Errorf("inspecting UI: %w", err)
	}
	count := 0
	for _, element := range elements {
		if stateElementMatches(element, o) {
			count++
		}
	}
	if o.Gone && count == 0 {
		return nil
	}
	if !o.Gone {
		if count > 1 {
			return fmt.Errorf("%d visible elements match; narrow the text or role condition", count)
		}
		if count == 1 {
			return nil
		}
	}
	return ErrConditionUnmet
}

// CheckWindowState observes an element condition once while retaining the selected identity.
func CheckWindowState(ctx context.Context, sess *Session, w *WindowInfo, o StateOptions) error {
	if o.Stable > 0 {
		return fmt.Errorf("a single check cannot establish pixel stability")
	}
	windows, err := sess.Window.List(ctx)
	if err != nil {
		return fmt.Errorf("listing windows: %w", err)
	}
	for _, current := range windows {
		if current.ID == w.ID {
			*w = current
			return checkElementState(ctx, sess, *w, o)
		}
	}
	if o.Gone {
		return nil
	}
	return fmt.Errorf("window %q disappeared", w.ID)
}
