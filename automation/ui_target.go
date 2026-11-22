package automation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WindowTarget applies independent filters; an exact ID takes precedence.
type WindowTarget struct {
	ID        string
	Match     string
	Active    bool
	Regex     bool
	MatchMode string
	Title     string
	Class     string
	PID       int
	Image     string
	All       bool
}

// SelectWindows preserves exact-ID precedence and permits absence for polling.
func SelectWindows(windows []WindowInfo, t WindowTarget) ([]WindowInfo, error) {
	if t.ID == "" && t.Match == "" && !t.Active && t.Title == "" && t.Class == "" && t.PID == 0 && t.Image == "" && !t.All {
		return nil, fmt.Errorf("a window selector is required")
	}
	if t.ID == "" && t.PID < 0 {
		return nil, fmt.Errorf("window pid must be nonnegative")
	}
	mode := t.MatchMode
	if mode == "" {
		mode = "contains"
		if t.Regex {
			mode = "regex"
		}
	}
	primary := t.Match
	if t.Active {
		primary = ""
	}
	match, err := textMatcher(primary, mode)
	if err != nil && t.ID == "" {
		return nil, err
	}
	title, titleErr := textMatcher(t.Title, mode)
	class, classErr := textMatcher(t.Class, mode)
	if t.ID == "" {
		if titleErr != nil {
			return nil, titleErr
		}
		if classErr != nil {
			return nil, classErr
		}
	}
	if t.ID == "" && !t.Active && t.MatchMode == "" && !t.Regex && t.Title == "" && t.Class == "" && t.PID == 0 && t.Image == "" {
		for _, w := range windows {
			if w.ID == t.Match {
				return []WindowInfo{w}, nil
			}
		}
	}
	out := make([]WindowInfo, 0)
	if t.ID != "" {
		// Provider IDs can be case-sensitive, so the fold that accepts a
		// hexadecimal ID typed in another case applies only when no ID matches
		// as written.
		for _, w := range windows {
			if w.ID == t.ID {
				out = append(out, w)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	for _, w := range windows {
		if t.ID != "" {
			if strings.EqualFold(w.ID, t.ID) {
				out = append(out, w)
			}
			continue
		}
		if t.Active && !w.Active {
			continue
		}
		if !t.Active && t.Match != "" && !match(w.Title) && !match(w.Class) && !(t.MatchMode == "" && !t.Regex && match(w.ID)) {
			continue
		}
		if !title(w.Title) || !class(w.Class) || (t.PID != 0 && t.PID != w.PID) {
			continue
		}
		if t.Image != "" && !matchesWindowImage(w.Image, t.Image) {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

// SelectWindow rejects absent and ambiguous targets before an action.
func SelectWindow(ctx context.Context, s *Session, t WindowTarget) (WindowInfo, error) {
	windows, err := s.Window.List(ctx)
	if err != nil {
		return WindowInfo{}, err
	}
	matched, err := SelectWindows(windows, t)
	if err != nil {
		return WindowInfo{}, err
	}
	if len(matched) != 1 {
		return WindowInfo{}, fmt.Errorf("%d windows match the target; select one window by id or a unique match", len(matched))
	}
	return matched[0], nil
}

// WaitWindow waits for an unambiguous presence or absence observation.
func WaitWindow(ctx context.Context, s *Session, t WindowTarget, gone bool) (*WindowInfo, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	observedUnmet := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		window, err := CheckWindow(ctx, s, t, gone)
		if err != ErrConditionUnmet {
			if observedUnmet && ctx.Err() != nil && contextErrorOnly(err, ctx.Err()) {
				return nil, ctx.Err()
			}
			return window, err
		}
		observedUnmet = true
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func textMatcher(value, mode string) (func(string) bool, error) {
	switch mode {
	case "regex":
		pattern, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("invalid window pattern %q: %w", value, err)
		}
		return pattern.MatchString, nil
	case "contains":
		return func(text string) bool { return strings.Contains(text, value) }, nil
	case "exact":
		return func(text string) bool { return value == "" || text == value }, nil
	default:
		return nil, fmt.Errorf("unknown window match mode %q", mode)
	}
}

func matchesWindowImage(image, target string) bool {
	image = strings.ReplaceAll(image, "\\", "/")
	target = strings.ReplaceAll(target, "\\", "/")
	if !strings.Contains(target, "/") {
		if i := strings.LastIndexByte(image, '/'); i >= 0 {
			image = image[i+1:]
		}
	}
	return image == target
}

// CheckWindow observes presence or absence once without treating provider failure as absence.
func CheckWindow(ctx context.Context, s *Session, t WindowTarget, gone bool) (*WindowInfo, error) {
	windows, err := s.Window.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing windows: %w", err)
	}
	matched, err := SelectWindows(windows, t)
	if err != nil {
		return nil, err
	}
	if !gone && len(matched) > 1 {
		return nil, fmt.Errorf("%d windows match the target; narrow the selection", len(matched))
	}
	if gone && len(matched) == 0 {
		return nil, nil
	}
	if !gone && len(matched) == 1 {
		return &matched[0], nil
	}
	return nil, ErrConditionUnmet
}

// contextErrorOnly excludes joined provider failures from normal wait expiry.
func contextErrorOnly(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
