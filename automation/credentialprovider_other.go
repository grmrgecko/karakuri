//go:build !windows

package automation

// The credential provider is a platform component: on Windows a COM DLL LogonUI
// loads on the secure desktop. Every other platform (macOS included, which
// signs in by typing rather than a loginwindow plugin) has no such component,
// so the ensure and remove are no-ops.

// EnsureUnlockProvider is a no-op on platforms without a secure-surface
// credential provider, and answers an empty status.
func EnsureUnlockProvider() (string, error) {
	return "", nil
}

// RemoveUnlockProvider is a no-op on platforms without a secure-surface
// credential provider.
func RemoveUnlockProvider() error {
	return nil
}
