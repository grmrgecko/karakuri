package automation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// DragRequest moves between absolute desktop coordinates while holding a button.
type DragRequest struct {
	StartX   int           `json:"start_x"`
	StartY   int           `json:"start_y"`
	EndX     int           `json:"end_x"`
	EndY     int           `json:"end_y"`
	Button   Button        `json:"button"`
	Duration time.Duration `json:"duration"`
}

func (r DragRequest) validate() error {
	if r.Button != "" && r.Button != "left" && r.Button != "middle" && r.Button != "right" {
		return fmt.Errorf("drag button must be left, middle, or right")
	}
	if r.Duration < 0 || r.Duration > 30*time.Second {
		return fmt.Errorf("drag duration must be between zero and thirty seconds")
	}
	for _, v := range []int{r.StartX, r.StartY, r.EndX, r.EndY} {
		if v < -1000000 || v > 1000000 {
			return fmt.Errorf("drag coordinates must be between -1000000 and 1000000")
		}
	}
	return nil
}

type inputGate chan struct{}

func (g inputGate) take(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil {
		return func() {}, nil
	}
	select {
	case g <- struct{}{}:
		return func() { <-g }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func wireInput(s *Session) {
	s.input = make(inputGate, 1)
	s.Mouse = serialMouse{s.Mouse, s.input}
	s.Keyboard = serialKeyboard{s.Keyboard, s.input}
	s.Window = serialWindow{s.Window, s.input}
}

type serialMouse struct {
	Mouse
	gate inputGate
}

func (m serialMouse) Move(ctx context.Context, x, y int, relative bool) error {
	release, err := m.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.Mouse.Move(ctx, x, y, relative)
}
func (m serialMouse) Click(ctx context.Context, b Button, n int) error {
	release, err := m.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.Mouse.Click(ctx, b, n)
}
func (m serialMouse) Hold(ctx context.Context, b Button, down bool) error {
	release, err := m.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.Mouse.Hold(ctx, b, down)
}
func (m serialMouse) Scroll(ctx context.Context, n int, h bool) error {
	release, err := m.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.Mouse.Scroll(ctx, n, h)
}

type serialKeyboard struct {
	Keyboard
	gate inputGate
}

func (k serialKeyboard) TypeText(ctx context.Context, c Clock, text string, delay time.Duration) error {
	release, err := k.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return k.Keyboard.TypeText(ctx, c, text, delay)
}
func (k serialKeyboard) PressKeys(ctx context.Context, key KeyStroke) error {
	release, err := k.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return k.Keyboard.PressKeys(ctx, key)
}

type serialWindow struct {
	Window
	gate inputGate
}

func (w serialWindow) Act(ctx context.Context, target WindowInfo, action WindowAction) error {
	release, err := w.gate.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return w.Window.Act(ctx, target, action)
}

func rawInputSession(s *Session) *Session {
	copy := *s
	if m, ok := s.Mouse.(serialMouse); ok {
		copy.Mouse = m.Mouse
	}
	if k, ok := s.Keyboard.(serialKeyboard); ok {
		copy.Keyboard = k.Keyboard
	}
	copy.Window = rawWindow(s.Window)
	return &copy
}

func rawWindow(w Window) Window {
	switch v := w.(type) {
	case serialWindow:
		return rawWindow(v.Window)
	case metadataWindow:
		v.Window = rawWindow(v.Window)
		return v
	default:
		return rawPlatformWindow(w)
	}
}

func (d localDesktop) Drag(ctx context.Context, r DragRequest) (err error) {
	if err = r.validate(); err != nil {
		return err
	}
	release, err := d.sess.input.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	mouse := rawInputSession(d.sess).Mouse
	button := r.Button
	if button == "" {
		button = "left"
	}
	if err = mouse.Move(ctx, r.StartX, r.StartY, false); err != nil {
		return err
	}
	// A failed press can still have delivered input before its reply was lost.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := mouse.Hold(cleanup, button, false); e != nil {
			err = errors.Join(err, fmt.Errorf("releasing drag button: %w", e))
		}
	}()
	if err = mouse.Hold(ctx, button, true); err != nil {
		return err
	}
	steps := int(r.Duration / (time.Second / 60))
	if steps < 1 {
		steps = 1
	}
	if steps > 900 {
		steps = 900
	}
	start := time.Now()
	for i := 1; i <= steps; i++ {
		due := start.Add(time.Duration(float64(r.Duration) * float64(i) / float64(steps)))
		if pause := time.Until(due); pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		x := r.StartX + int(math.Round(float64(r.EndX-r.StartX)*float64(i)/float64(steps)))
		y := r.StartY + int(math.Round(float64(r.EndY-r.StartY)*float64(i)/float64(steps)))
		if err = mouse.Move(ctx, x, y, false); err != nil {
			return err
		}
	}
	return nil
}
