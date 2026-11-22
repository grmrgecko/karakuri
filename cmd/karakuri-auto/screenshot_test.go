package main

import (
	"testing"

	"github.com/grmrgecko/karakuri/automation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseScreenshotOptions pins the screenshot command's argument format,
// including signed coordinates, option ordering, and mutually exclusive modes.
func TestParseScreenshotOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    screenshotOptions
		invalid bool
	}{
		{name: "default", want: screenshotOptions{path: "karakuri-auto.png"}},
		{name: "legacy file", args: []string{"capture.png"}, want: screenshotOptions{path: "capture.png"}},
		{name: "region after file", args: []string{"crop.png", "--region=-10,20,300,200", "--display", "1"}, want: screenshotOptions{path: "crop.png", request: automation.CaptureRequest{Region: true, Display: 1, X: -10, Y: 20, Width: 300, Height: 200}}},
		{name: "window title", args: []string{"--window", "Some title", "w.png"}, want: screenshotOptions{path: "w.png", window: "Some title"}},
		{name: "active", args: []string{"--active-window"}, want: screenshotOptions{path: "karakuri-auto.png", active: true}},
		{name: "escaped file", args: []string{"--", "--window"}, want: screenshotOptions{path: "--window"}},
		{name: "missing value", args: []string{"--region"}, invalid: true},
		{name: "short region", args: []string{"--region", "1,2,3"}, invalid: true},
		{name: "noninteger", args: []string{"--region", "x,2,3,4"}, invalid: true},
		{name: "zero width", args: []string{"--region", "1,2,0,4"}, invalid: true},
		{name: "negative height", args: []string{"--region", "1,2,3,-4"}, invalid: true},
		{name: "mixed modes", args: []string{"--region", "1,2,3,4", "--active-window"}, invalid: true},
		{name: "two windows", args: []string{"--window", "one", "--active-window"}, invalid: true},
		{name: "window display", args: []string{"--window", "one", "--display", "0"}, invalid: true},
		{name: "negative display", args: []string{"--display", "-1"}, invalid: true},
		{name: "empty window", args: []string{"--window="}, invalid: true},
		{name: "duplicate region", args: []string{"--region=1,2,3,4", "--region=2,3,4,5"}, invalid: true},
		{name: "extra file", args: []string{"a.png", "b.png"}, invalid: true},
		{name: "unknown flag", args: []string{"--unknown"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseScreenshotOptions(tc.args)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestScreenshotWindowSelection keeps duplicate titles from selecting an
// arbitrary window and gives an exact ID priority over substring matches.
func TestScreenshotWindowSelection(t *testing.T) {
	windows := []automation.WindowInfo{
		{ID: "0x123", Title: "Notes", Class: "Editor"},
		{ID: "0x1234", Title: "Notes", Class: "Editor", Active: true},
	}
	for _, tc := range []struct {
		match   string
		active  bool
		want    string
		invalid bool
	}{
		{match: "0x123", want: "0x123"},
		{match: "Notes", invalid: true},
		{match: "missing", invalid: true},
		{active: true, want: "0x1234"},
	} {
		got, err := screenshotWindow(windows, tc.match, tc.active)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tc.want, got.ID)
	}
}
