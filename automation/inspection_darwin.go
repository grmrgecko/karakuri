//go:build darwin

package automation

import "github.com/grmrgecko/karakuri/automation/internal/macos"

const inspectionBackend = "accessibility/coregraphics"

func newInspection() Inspection { return macos.Inspection{} }
