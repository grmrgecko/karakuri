//go:build windows

package exec

import "os"

// writeScript writes the script file. Windows runs the interpreter with the
// script as an argument, and child processes there inherit only the handles
// they are given.
func writeScript(path string, data []byte) error {
	return os.WriteFile(path, data, 0o700)
}
