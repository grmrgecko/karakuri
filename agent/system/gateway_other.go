//go:build !linux && !darwin && !windows

package system

func defaultGateway() map[string]any { return nil }
