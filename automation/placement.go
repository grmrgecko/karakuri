package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// WindowPlacement requests position or size without assuming the window manager honors it.
type WindowPlacement = autoapi.WindowPlacement

type windowPlacer interface {
	Place(context.Context, WindowInfo, WindowPlacement) error
}

func (d localDesktop) PlaceWindow(ctx context.Context, w WindowInfo, p WindowPlacement) error {
	if err := p.Validate(); err != nil {
		return err
	}
	release, err := d.sess.input.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	window := rawInputSession(d.sess).Window
	placer, ok := window.(windowPlacer)
	if !ok {
		return fmt.Errorf("window placement: %w", ErrUnsupported)
	}
	return placer.Place(ctx, w, p)
}

func (w metadataWindow) Place(ctx context.Context, target WindowInfo, p WindowPlacement) error {
	if placer, ok := w.Window.(windowPlacer); ok {
		return placer.Place(ctx, target, p)
	}
	return fmt.Errorf("window placement: %w", ErrUnsupported)
}

// WindowPlacementResult separates a requested layout from freshly observed geometry.
type WindowPlacementResult struct {
	Requested     WindowPlacement    `json:"requested"`
	Window        SnapshotWindowInfo `json:"window"`
	BoundsMatched *bool              `json:"bounds_matched,omitempty"`
}

// PlaceAndObserveWindow waits briefly for a window-manager request to settle.
// BoundsMatched reports clamping or refusal without claiming exact placement.
func PlaceAndObserveWindow(ctx context.Context, s *Session, w WindowInfo, p WindowPlacement) (WindowPlacementResult, error) {
	result := WindowPlacementResult{Requested: p}
	if err := s.Desktop.PlaceWindow(ctx, w, p); err != nil {
		return result, err
	}
	until := time.Now().Add(2 * time.Second)
	for {
		current, err := SelectWindow(ctx, s, WindowTarget{ID: w.ID})
		if err != nil {
			return result, err
		}
		result.Window = NewWindowSnapshot(s, current).Window
		if !p.Move && !p.Resize {
			return result, nil
		}
		matched := current.Width > 0 && current.Height > 0 && (!p.Move || (current.X == p.X && current.Y == p.Y)) && (!p.Resize || (current.Width == p.Width && current.Height == p.Height))
		result.BoundsMatched = &matched
		if matched || !time.Now().Before(until) {
			return result, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}
