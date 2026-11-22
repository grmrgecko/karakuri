//go:build freebsd

package automation

import (
	"context"
	"os"
	"os/user"
	"strconv"

	"github.com/grmrgecko/karakuri/automation/desktopenv"
)

// FreeBSD has no logind to name the desktop session, which is the same reason
// its session listing falls back to utmp. A root delegate therefore finds the
// desktop the way the same-user attach does — by reading a graphical process's
// environment — only it first has to discover which user owns one. It scans for
// a live graphical session belonging to a real account and delegates to that
// user. Unlike the Linux path, there is no session manager to name the active
// seat among several, so the choice is deterministic rather than a pick of the
// one physically in front of the screen; DiscoverAny documents how it decides.
func findDelegateTarget(ctx context.Context, opts Options) (DelegateTarget, bool) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(opts.HelperCommand) == 0 {
		return DelegateTarget{}, false
	}
	if os.Geteuid() != 0 {
		return findSameUserAttach(ctx, logf)
	}

	uid, res, ok := desktopenv.DiscoverAny(ctx)
	if !ok {
		logf("running as root and found no graphical session to delegate to yet; the session helper starts when a user logs in")
		return pendingTarget(), true
	}
	target, err := buildAttachTarget(uid, res)
	if err != nil {
		logf("preparing the session helper for uid %d: %v", uid, err)
		return DelegateTarget{}, false
	}
	logf("delegating desktop capabilities to %s", target)
	return target, true
}

// wirePerSessionHelpers is a no-op on FreeBSD: the helper the composition
// already spawns is the session's own, and there is no second session to reach
// the way macOS reaches the login window.
func wirePerSessionHelpers(s *Session, opts Options, target DelegateTarget, h *helper) {}

// lookupSessionUser resolves the account fields a helper needs to be uid. Go's
// cgo-free os/user reads the password database directly, which is the whole of
// FreeBSD's account store for a local desktop user.
func lookupSessionUser(uid int) (name, home string, gid int, groups []uint32, err error) {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", "", 0, nil, err
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return "", "", 0, nil, err
	}
	// Supplementary groups carry device access — a desktop user's membership in
	// video, webcam, or wheel — so an empty list would silently strip it.
	ids, err := u.GroupIds()
	if err != nil {
		return u.Username, u.HomeDir, gid, []uint32{uint32(gid)}, nil
	}
	groups = make([]uint32, 0, len(ids))
	for _, id := range ids {
		if n, err := strconv.Atoi(id); err == nil {
			groups = append(groups, uint32(n))
		}
	}
	if len(groups) == 0 {
		groups = []uint32{uint32(gid)}
	}
	return u.Username, u.HomeDir, gid, groups, nil
}
