//go:build darwin

package macos

import (
	"context"
	"fmt"
	"math"
	"time"
)

// The pointer is driven through CoreGraphics events rather than cliclick, so it
// needs nothing installed. A click is a down and an up at the current location;
// a move posts a mouse-moved event so applications that track the pointer see
// it, rather than warping the cursor silently.

// clickHold is how long a click holds the button down. A down and an up posted
// back to back reach some applications as one instant: the iOS Simulator turns
// them into a touch that begins and is never seen to end, leaving the control
// pressed. A physical click holds the button for tens of milliseconds.
const clickHold = 40 * time.Millisecond

// clickGap separates the clicks of a double or triple click, well inside the
// system's double-click interval.
const clickGap = 60 * time.Millisecond

// buttonForName maps a canonical button to its CoreGraphics number and the
// down/up event types for it.
func buttonForName(button string) (num uint32, down, up uint32, err error) {
	switch button {
	case "", buttonLeft:
		return kCGMouseButtonLeft, kCGEventLeftMouseDown, kCGEventLeftMouseUp, nil
	case buttonRight:
		return kCGMouseButtonRight, kCGEventRightMouseDown, kCGEventRightMouseUp, nil
	case buttonMiddle:
		return kCGMouseButtonCenter, kCGEventOtherMouseDown, kCGEventOtherMouseUp, nil
	default:
		return 0, 0, 0, fmt.Errorf("unknown mouse button %q", button)
	}
}

// currentPoint reads where the pointer is now.
func currentPoint() (cgPoint, error) {
	event := cgEventCreate(0)
	if event == 0 {
		return cgPoint{}, fmt.Errorf("macOS refused to report the pointer position")
	}
	defer cfRelease(event)
	return cgEventGetLocation(event), nil
}

// moveMouse moves the pointer, absolutely or by an offset from where it is.
func moveMouse(ctx context.Context, x, y int, relative bool) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	target := cgPoint{X: float64(x), Y: float64(y)}
	if relative {
		at, err := currentPoint()
		if err != nil {
			return err
		}
		target = cgPoint{X: at.X + float64(x), Y: at.Y + float64(y)}
	}
	if err := postMouse(kCGEventMouseMoved, target, kCGMouseButtonLeft, 0); err != nil {
		return err
	}
	// CGEventPost queues the move. A following click reads the current pointer
	// position, so returning before the move arrives can click the old location.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		at, err := currentPoint()
		if err != nil {
			return err
		}
		if math.Abs(at.X-target.X) < 1 && math.Abs(at.Y-target.Y) < 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pointer did not reach (%g, %g): %w", target.X, target.Y, ctx.Err())
		case <-ticker.C:
		}
	}
}

// clickMouse clicks the named button where the pointer is, count times.
func clickMouse(ctx context.Context, button string, count int) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	num, down, up, err := buttonForName(button)
	if err != nil {
		return err
	}
	at, err := currentPoint()
	if err != nil {
		return err
	}
	if count < 1 {
		count = 1
	}
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, clickGap); err != nil {
				return err
			}
		}
		// Each pair carries its click count, as a physical mouse's do, so a
		// double click reads as one rather than as two single clicks.
		state := int64(i + 1)
		if err := postMouse(down, at, num, state); err != nil {
			return err
		}
		if err := sleepCtx(ctx, clickHold); err != nil {
			// The button is down; release it before giving up.
			_ = postMouse(up, at, num, state)
			return err
		}
		if err := postMouse(up, at, num, state); err != nil {
			return err
		}
	}
	return nil
}

// holdMouse presses a button down or releases it where the pointer is.
func holdMouse(ctx context.Context, button string, down bool) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	num, downType, upType, err := buttonForName(button)
	if err != nil {
		return err
	}
	at, err := currentPoint()
	if err != nil {
		return err
	}
	eventType := upType
	if down {
		eventType = downType
	}
	return postMouse(eventType, at, num, 1)
}

// sleepCtx waits for d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// scrollMouse turns the wheel, which cliclick could not do at all. The amount
// is in lines: positive scrolls up, or right when horizontal.
func scrollMouse(ctx context.Context, amount int, horizontal bool) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	var vertical, horiz int32
	if horizontal {
		horiz = int32(amount)
	} else {
		vertical = int32(amount)
	}
	event := cgEventCreateScrollWheelEvent(0, kCGScrollEventUnitLine, 2, vertical, horiz)
	if event == 0 {
		return fmt.Errorf("macOS refused to create a scroll event")
	}
	defer cfRelease(event)
	cgEventPost(kCGHIDEventTap, event)
	return nil
}

// mousePoint reports where the pointer is, as integer screen coordinates.
func mousePoint(ctx context.Context) (int, int, error) {
	if err := load(); err != nil {
		return 0, 0, err
	}
	at, err := currentPoint()
	if err != nil {
		return 0, 0, err
	}
	return int(at.X), int(at.Y), nil
}

// postMouse creates one mouse event and posts it. A button event carries its
// click count in clickState; a move passes 0 and sets none.
func postMouse(eventType uint32, at cgPoint, button uint32, clickState int64) error {
	event := cgEventCreateMouseEvent(0, eventType, at, button)
	if event == 0 {
		return fmt.Errorf("macOS refused to create a mouse event")
	}
	defer cfRelease(event)
	if clickState > 0 {
		cgEventSetIntegerValueField(event, kCGMouseEventClickState, clickState)
	}
	cgEventPost(kCGHIDEventTap, event)
	return nil
}
