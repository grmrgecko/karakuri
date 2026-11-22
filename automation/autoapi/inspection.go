package autoapi

import "context"

// Element identifies a control in a window's accessibility tree. IDs belong to
// the provider and must be re-enumerated after the application rebuilds its UI.
type Element struct {
	ID            string   `json:"id"`
	Parent        string   `json:"parent,omitempty"`
	Name          string   `json:"name"`
	Text          string   `json:"text,omitempty"`
	Role          string   `json:"role"`
	Class         string   `json:"class,omitempty"`
	Path          string   `json:"path,omitempty"`
	X             int      `json:"x"`
	Y             int      `json:"y"`
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	Enabled       bool     `json:"enabled"`
	Visible       bool     `json:"visible"`
	Protected     bool     `json:"protected,omitempty"`
	GeometryError string   `json:"geometry_error,omitempty"`
	Actions       []string `json:"actions,omitempty"`
}

// DisplayInfo describes an active output in desktop coordinates.
type DisplayInfo struct {
	ID      string  `json:"id"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Depth   int     `json:"depth"`
	Refresh float64 `json:"refresh"`
	Primary bool    `json:"primary"`
}

// DisplayMode selects a mode of the primary output, leaving other outputs alone.
type DisplayMode struct{ Width, Height, Depth int }

// Inspection reads structured UI and invokes actions exposed by its provider.
// Display changes are temporary and can be rejected by the compositor or driver.
type Inspection interface {
	Elements(context.Context, WindowInfo) ([]Element, error)
	Invoke(context.Context, WindowInfo, string) error
	Displays(context.Context) ([]DisplayInfo, error)
	SetDisplay(context.Context, DisplayMode) error
}
