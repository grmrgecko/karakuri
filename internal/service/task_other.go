//go:build !windows

package service

import "fmt"

// taskScoped is false everywhere but Windows: every other platform's user
// scope is a real per-user service manager.
func taskScoped(Definition) bool {
	return false
}

// userTaskControl is unreachable off Windows; taskScoped never routes here.
func userTaskControl(def Definition, action string) error {
	return fmt.Errorf("scheduled tasks are Windows-only")
}
