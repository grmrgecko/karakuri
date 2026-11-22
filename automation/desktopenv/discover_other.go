//go:build !linux && !freebsd

package desktopenv

import "context"

// Discover reports that this platform has no environment-scraping attach path.
// macOS joins a session through launchd and Windows through the console user's
// token, neither of which is reached by copying display variables, so there is
// nothing to discover here.
func Discover(ctx context.Context, uid int) (Result, bool) {
	return Result{}, false
}
