//go:build !windows

package service

// hideOwnConsole exists for the Windows scheduled-task launch; nothing else
// allocates a console to hide.
func hideOwnConsole() {}
