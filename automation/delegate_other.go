//go:build !linux && !darwin && !freebsd && !windows

package automation

import "context"

// findDelegateTarget reports that this platform does not delegate.
//
// Windows does not have the problem Linux and macOS have, and could not solve
// it the same way if it did: a service runs in session 0, which has no desktop
// at all and no way to reach one — interactive automation on Windows is
// installed as a logon task instead, which is already how a user-scope install
// works there.
//
// Linux, FreeBSD, and macOS have their own delegate files; what remains here is
// the BSDs and the odd targets with neither a session manager nor a way to scrape
// a graphical session's environment, where nothing can be delegated to.
func findDelegateTarget(ctx context.Context, opts Options) (DelegateTarget, bool) {
	return DelegateTarget{}, false
}

// wirePerSessionHelpers is a no-op on the platforms that never delegate.
func wirePerSessionHelpers(s *Session, opts Options, target DelegateTarget, h *helper) {}
