//go:build linux || freebsd || openbsd || netbsd || dragonfly

// Package utmpsess is the session listing for a Unix host with no session
// manager to ask. FreeBSD is the case, and a Linux host without systemd is the
// other: nothing tracks whether a session is locked, nothing can activate one,
// and nothing can end somebody else's, so this reports who is logged in and
// says plainly that the rest is not there.
//
// The listing comes from utmp through who(1) rather than by parsing
// /var/run/utx.active directly: the record layout differs between the BSDs and
// changes between releases, while who's output has been the same three columns
// for decades. The agent's own session is merged in, because a display manager
// that writes no utmp record — xdm is one — would otherwise leave the desktop
// nobody's session.
package utmpsess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// Sessions is the utmp-backed session capability.
type Sessions struct{}

// New returns the fallback session backend.
func New() *Sessions { return &Sessions{} }

// List reports the logged-in users utmp knows about, plus the agent's own
// session when utmp missed it.
func (s *Sessions) List(ctx context.Context) ([]autoapi.SessionInfo, error) {
	// A host without who has no utmp rows to add, and the process's own
	// session below is still an answer.
	out, err := toolsh.RunTool(ctx, "who")
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		return nil, err
	}

	seen := make(map[string]bool)
	var sessions []autoapi.SessionInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, tty := fields[0], fields[1]
		key := name + "@" + tty
		if seen[key] {
			continue
		}
		seen[key] = true
		sessions = append(sessions, describe(name, tty))
	}

	if own := ownSession(); own.User != "" && !markOwn(sessions, own) {
		sessions = append(sessions, own)
	}
	return sessions, nil
}

// markOwn finds the utmp row for the X session the agent runs in and marks it
// active, reporting whether there was one. Only an X display can be matched:
// a display manager records it in the line column, while a Wayland or terminal
// session leaves nothing in utmp that names the agent's own.
func markOwn(sessions []autoapi.SessionInfo, own autoapi.SessionInfo) bool {
	if own.Type != "x11" {
		return false
	}
	for i := range sessions {
		row := &sessions[i]
		if row.User == own.User && row.Type == "x11" && displayServer(row.Display) == displayServer(own.Display) {
			row.Active = true
			row.State = own.State
			return true
		}
	}
	return false
}

// displayServer drops the screen from an X display name, so the ":0.0" a
// client carries in DISPLAY matches the ":0" a display manager writes to utmp.
func displayServer(display string) string {
	colon := strings.LastIndexByte(display, ':')
	if colon < 0 {
		return display
	}
	number, _, _ := strings.Cut(display[colon+1:], ".")
	return display[:colon+1] + number
}

// describe builds one session row from a utmp user and line.
func describe(name, tty string) autoapi.SessionInfo {
	info := autoapi.SessionInfo{
		ID:    name + "@" + tty,
		User:  name,
		UID:   lookupUID(name),
		TTY:   tty,
		Class: autoapi.SessionClassUser,
		State: "online",
		// Nothing on a host without a session manager reports a lock, which is
		// different from reporting that the screen is unlocked.
		LockKnown: false,
	}
	// A display manager records the X display in the line field, so a line
	// starting with a colon is a graphical session rather than a terminal.
	if strings.HasPrefix(tty, ":") {
		info.Type = "x11"
		info.Display = tty
		info.TTY = ""
	} else {
		info.Type = "tty"
	}
	return info
}

// ownSession describes the session the agent itself runs in, read from the
// environment. It is what fills the gap when the display manager wrote no utmp
// record for the desktop.
func ownSession() autoapi.SessionInfo {
	u, err := user.Current()
	if err != nil {
		return autoapi.SessionInfo{}
	}
	info := autoapi.SessionInfo{
		ID:     "self",
		User:   u.Username,
		UID:    lookupUID(u.Username),
		Class:  autoapi.SessionClassUser,
		State:  "active",
		Active: true,
	}
	switch {
	case strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "":
		info.Type = "wayland"
		info.Display = os.Getenv("WAYLAND_DISPLAY")
	case strings.TrimSpace(os.Getenv("DISPLAY")) != "":
		info.Type = "x11"
		info.Display = os.Getenv("DISPLAY")
	default:
		info.Type = "tty"
	}
	return info
}

// lookupUID resolves a user name to its uid, reporting -1 for a name the
// password database does not carry.
func lookupUID(name string) int {
	u, err := user.Lookup(name)
	if err != nil {
		return -1
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return -1
	}
	return uid
}

// Act reports that this host offers none of the session actions. Every one of
// them is a session manager's job, and the fallback exists precisely because
// there is no session manager to ask — approximating a logout by killing the
// user's processes would be a different action wearing this one's name.
func (s *Sessions) Act(ctx context.Context, target autoapi.SessionInfo, action autoapi.SessionAction) error {
	verb := string(action) + " a session"
	switch action {
	case autoapi.SessionGreeter:
		verb = "show the login window"
	case autoapi.SessionLogout:
		verb = "log out a session"
	}
	return fmt.Errorf("%w: this host runs no session manager, so it cannot %s; "+
		"the Power node still locks and logs out the session the agent runs in",
		autoapi.ErrUnsupported, verb)
}

// Login reports that a credentialed unlock is not available here. The password
// could be checked, but there is no session manager to dismiss a lock through,
// so verifying it would be theatre.
func (s *Sessions) Login(ctx context.Context, req autoapi.LoginRequest) error {
	return fmt.Errorf("%w: this host runs no session manager, so it cannot perform a credentialed unlock",
		autoapi.ErrUnsupported)
}
