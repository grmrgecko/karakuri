package automation

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWindowSelectorContract pins regex parsing, literal matching, and independent process constraints.
func TestWindowSelectorContract(t *testing.T) {
	windows := []WindowInfo{
		{ID: "0x1", Title: "Setup [1]", Class: "Dialog", PID: 42, Image: `C:\Apps\setup.exe`},
		{ID: "0x2", Title: "Setup [1]", Class: "Browser", Active: true, PID: 43, Image: "/usr/bin/browser"},
	}
	for _, tc := range []struct {
		name    string
		target  WindowTarget
		ids     []string
		invalid bool
	}{
		{name: "literal title", target: WindowTarget{Title: "Setup [1]", MatchMode: "exact"}, ids: []string{"0x1", "0x2"}},
		{name: "independent constraints", target: WindowTarget{Title: "Setup", Class: "Dialog", MatchMode: "contains", PID: 42, Image: "setup.exe"}, ids: []string{"0x1"}},
		{name: "regex title and class", target: WindowTarget{Match: "^Dialog$", Regex: true}, ids: []string{"0x1"}},
		{name: "active precedence", target: WindowTarget{Active: true, Match: "[", Regex: true}, ids: []string{"0x2"}},
		{name: "id precedence", target: WindowTarget{ID: "0x2", Title: "[", PID: 42, Regex: true}, ids: []string{"0x2"}},
		{name: "invalid regex", target: WindowTarget{Title: "[", Regex: true}, invalid: true},
		{name: "invalid mode", target: WindowTarget{Title: "Setup", MatchMode: "glob"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := SelectWindows(windows, tc.target)
			if tc.invalid {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			ids := make([]string, 0, len(matched))
			for _, w := range matched {
				ids = append(ids, w.ID)
			}
			assert.Equal(t, tc.ids, ids)
		})
	}
}

// TestSelectWindowsPrefersExactIDCase locks an exact window ID to one window
// when another ID differs only by letter case. The Wayland foreign-toplevel
// backend builds IDs from base64, which is case-sensitive, so two windows such
// as "Hash" and "bash" in one terminal have IDs that fold to the same text.
// Hexadecimal IDs typed in another case must still match.
func TestSelectWindowsPrefersExactIDCase(t *testing.T) {
	windows := []WindowInfo{
		{ID: "wl:Zm9vdABIYXNo", Title: "Hash", Class: "foot"},
		{ID: "wl:Zm9vdABiYXNo", Title: "bash", Class: "foot"},
		{ID: "0x3a00007", Title: "Editor", Class: "editor"},
	}
	for _, tc := range []struct {
		name, id string
		titles   []string
	}{
		{"exact case selects one window", "wl:Zm9vdABiYXNo", []string{"bash"}},
		{"other case of a hexadecimal ID still matches", "0x3A00007", []string{"Editor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := SelectWindows(windows, WindowTarget{ID: tc.id})
			require.NoError(t, err, "An ID target needs no other selector.")
			titles := make([]string, 0, len(matched))
			for _, w := range matched {
				titles = append(titles, w.Title)
			}
			assert.Equal(t, tc.titles, titles, "An exact ID must not also select a window whose ID differs by case.")
		})
	}
}

// TestWaitExpiryClassification prevents errors.Is from hiding joined provider failures.
func TestWaitExpiryClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		expiry bool
	}{
		{name: "deadline", err: context.DeadlineExceeded, expiry: true},
		{name: "wrapped deadline", err: fmt.Errorf("desktop read: %w", context.DeadlineExceeded), expiry: true},
		{name: "deadline and provider failure", err: errors.Join(context.DeadlineExceeded, errors.New("provider unavailable")), expiry: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.err, context.DeadlineExceeded)
			assert.Equal(t, tc.expiry, contextErrorOnly(tc.err, context.DeadlineExceeded))
		})
	}
}
