//go:build windows

package service

import "golang.org/x/sys/windows"

// Privileged reports whether this process can register the system's services.
// Windows has no uid zero; what gates the service manager is an elevated
// token, which is what an administrator prompt runs commands with.
func Privileged() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
