//go:build !darwin

package service

// EnsureStableSignature is a macOS concern: only there is a capability grant
// keyed to a binary's code signature. Everywhere else it does nothing.
func EnsureStableSignature(binaryPath string) string { return "" }
