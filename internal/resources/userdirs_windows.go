//go:build windows

package resources

import "os"

// userConfigBase is %AppData%, the roaming profile, which is where Windows
// keeps per-user configuration. The environment variable rather than a path
// derived from the home directory, because a profile with a relocated
// AppData moves the variable and not the home.
func userConfigBase() (string, bool) {
	if dir := os.Getenv("AppData"); dir != "" {
		return dir, true
	}
	return "", false
}

// userDataBase is %LocalAppData%, the machine-local half of the profile,
// which is where state belongs: roaming synchronizes a profile between
// machines, and a SQLite database that roams is a database that corrupts.
func userDataBase() (string, bool) {
	if dir := os.Getenv("LocalAppData"); dir != "" {
		return dir, true
	}
	return "", false
}
