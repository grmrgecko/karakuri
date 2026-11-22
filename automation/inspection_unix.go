//go:build !windows && !darwin

package automation

import "github.com/grmrgecko/karakuri/automation/internal/inspectunix"

const inspectionBackend = "at-spi/desktop-display"

func newInspection() Inspection { return inspectunix.Backend{} }
