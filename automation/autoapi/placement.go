package autoapi

import "fmt"

// WindowPlacement describes independently requested restore, position, and size.
type WindowPlacement struct {
	Restore bool `json:"restore"`
	Move    bool `json:"move"`
	Resize  bool `json:"resize"`
	X       int  `json:"x"`
	Y       int  `json:"y"`
	Width   int  `json:"width"`
	Height  int  `json:"height"`
}

// Validate rejects invalid placement before any window action is sent.
func (p WindowPlacement) Validate() error {
	if !p.Restore && !p.Move && !p.Resize {
		return fmt.Errorf("window placement needs restore, move, or resize")
	}
	if p.Move && (p.X < -1000000 || p.X > 1000000 || p.Y < -1000000 || p.Y > 1000000) {
		return fmt.Errorf("window position must be between -1000000 and 1000000")
	}
	if p.Resize && (p.Width <= 0 || p.Height <= 0 || p.Width > 100000 || p.Height > 100000) {
		return fmt.Errorf("window dimensions must be between 1 and 100000")
	}
	return nil
}
