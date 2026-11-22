//go:build darwin

package automation

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
	"github.com/grmrgecko/karakuri/automation/internal/macos"
)

// Delegation on macOS exists because a privileged process cannot reach a
// desktop session it does not belong to, which is the inverse of the Linux
// problem and the same conclusion. A root agent reads the session table and
// locks a screen, and is even trusted for Accessibility once the operator has
// granted its binary, yet its synthesized keys go nowhere: the window server
// delivers events to the session the process belongs to, and root belongs to
// none. The way to type into the console session is to be in it, and being in
// it means being started in it — so the helper is started there, with
// `launchctl bootstrap gui/<uid>` putting this same binary into the user's
// graphical session as a LaunchAgent.
//
// launchd-spawned jobs carry no inherited pipes, so the wire runs over a
// private unix socket the helper binds inside a directory only its user (and
// the privileged parent) can reach.

// findDelegateTarget selects a GUI session for root or an SSH caller. An
// unprivileged SSH caller can delegate only to its own graphical session.
// Delegation is opt-in: a caller that named no helper command gets none, because re-executing
// somebody's binary with arguments they did not choose is not a decision this
// module makes for them.
func findDelegateTarget(ctx context.Context, opts Options) (DelegateTarget, bool) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(opts.HelperCommand) == 0 {
		return DelegateTarget{}, false
	}
	uid := os.Geteuid()
	if uid != 0 && os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == "" {
		return DelegateTarget{}, false
	}

	sessions, err := macos.NewSessions().List(ctx)
	if err != nil {
		logf("listing sessions to find a desktop to delegate to: %v", err)
		return DelegateTarget{}, false
	}
	if uid != 0 {
		owned := sessions[:0]
		for _, session := range sessions {
			if session.UID == uid {
				owned = append(owned, session)
			}
		}
		sessions = owned
	}
	chosen, ok := pickDesktopSession(sessions)
	if !ok {
		logf("no graphical session available for desktop delegation yet; the session helper starts when one is")
		return pendingTarget(), true
	}

	target, err := buildMacOSTarget(chosen)
	if err != nil {
		logf("preparing the session helper for %s: %v", chosen.User, err)
		return DelegateTarget{}, false
	}
	logf("delegating desktop capabilities to %s", target)
	return target, true
}

// sessionHelperFactory returns the source of per-session helpers the macOS
// session backend uses to reach sessions the process does not belong to: for
// a session it spawns this binary as that session's owner and returns the
// helper's actor and keyboard. It is nil unless the caller opted into
// delegation and the process is root.
func sessionHelperFactory(opts Options) func(autoapi.SessionInfo) (macos.SessionHelpers, error) {
	if len(opts.HelperCommand) == 0 || os.Geteuid() != 0 {
		return nil
	}
	return func(info autoapi.SessionInfo) (macos.SessionHelpers, error) {
		if info.UID <= 0 {
			return macos.SessionHelpers{}, fmt.Errorf("no user session to spawn a helper into")
		}
		target, err := buildMacOSTarget(info)
		if err != nil {
			return macos.SessionHelpers{}, err
		}
		h := newHelper(func(ctx context.Context) (*hrpc.Conn, func(), error) {
			return spawnHelper(ctx, opts, target)
		}, opts.Logf)
		return macos.SessionHelpers{
			Sessions: helperSessions{h},
			Keyboard: helperKeyboard{h},
			Release:  func() { _ = h.Close() },
		}, nil
	}
}

// wirePerSessionHelpers hands the session backend the per-session helper
// factory. The startup delegate's own session reuses the helper already
// composed, and any other session gets a fresh one on demand — which is how
// the login-window path types credentials into sessions this process does
// not belong to.
func wirePerSessionHelpers(s *Session, opts Options, target DelegateTarget, h *helper) {
	if sf, ok := s.Sessions.(interface {
		SetHelperFactory(func(autoapi.SessionInfo) (macos.SessionHelpers, error))
	}); ok {
		inner := sessionHelperFactory(opts)
		if inner != nil {
			sf.SetHelperFactory(func(info autoapi.SessionInfo) (macos.SessionHelpers, error) {
				if info.UID == target.UID {
					return macos.SessionHelpers{
						Sessions: helperSessions{h},
						Keyboard: helperKeyboard{h},
						Release:  func() { _ = h.Close() },
					}, nil
				}
				return inner(info)
			})
		}
	}
}

// buildMacOSTarget assembles what the helper needs to be that user in that
// session. The launchd domain supplies the session context itself, so the
// environment is only the account basics; unlike Linux there is no display to
// scrape, because a graphical session on macOS is one thing and launchd is
// already inside it.
func buildMacOSTarget(info SessionInfo) (DelegateTarget, error) {
	u, err := user.LookupId(strconv.Itoa(info.UID))
	if err != nil {
		return DelegateTarget{}, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return DelegateTarget{}, err
	}

	env := map[string]string{
		"HOME":    u.HomeDir,
		"USER":    u.Username,
		"LOGNAME": u.Username,
		"SHELL":   "/bin/zsh",
		"PATH":    "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
	}
	target := DelegateTarget{
		UID:       info.UID,
		GID:       gid,
		User:      u.Username,
		Home:      u.HomeDir,
		SessionID: info.ID,
		Display:   "aqua",
	}
	if groups, err := u.GroupIds(); err == nil {
		for _, g := range groups {
			if n, err := strconv.Atoi(g); err == nil {
				target.Groups = append(target.Groups, uint32(n))
			}
		}
	}
	target.Env = make([]string, 0, len(env))
	for key, value := range env {
		if value != "" {
			target.Env = append(target.Env, key+"="+value)
		}
	}
	return target, nil
}

// spawnHelper starts this binary again as the target user, in that user's
// graphical session, and returns the framed socket to it. bootstrap is what
// puts the process in the session: a plain child of the privileged parent
// would keep the parent's own (empty) window-server context, and its events
// would be delivered nowhere.
func spawnHelper(ctx context.Context, opts Options, target DelegateTarget) (*hrpc.Conn, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("finding this binary to start the session helper: %w", err)
	}

	// A directory only the user and root can reach: the helper binds its
	// socket here, so the wire is private to the two of them.
	dir, err := os.MkdirTemp("", "karakuri-helper-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating the session helper's work directory: %w", err)
	}
	sock := filepath.Join(dir, "helper.sock")
	label := opts.appID() + ".session-helper." + filepath.Base(dir)
	plist := filepath.Join(dir, "helper.plist")
	// The plist is written while the directory is still root's own. Once the
	// user owns it they can swap any name in it for a symlink, and a root
	// write into it after that could be steered at any file on the system.
	if err := writeHelperPlist(plist, label, exe, opts, sock, target); err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	if err := os.Chown(dir, target.UID, target.GID); err != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("handing the session helper's directory to %s: %w", target, err)
	}

	domain := "gui/" + strconv.Itoa(target.UID)
	stopJob := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "launchctl", "bootout", domain+"/"+label).Run()
		_ = os.RemoveAll(dir)
	}
	if out, err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
		stopJob()
		return nil, nil, fmt.Errorf("starting the session helper for %s: %s: %w", target, strings.TrimSpace(string(out)), err)
	}

	conn, err := dialHelper(ctx, sock)
	if err != nil {
		stopJob()
		return nil, nil, err
	}

	stop := func() {
		_ = conn.Close()
		stopJob()
	}
	return hrpc.NewConn(conn, conn), stop, nil
}

// dialHelper connects to the helper's socket, retrying while launchd is still
// bringing the job up.
func dialHelper(ctx context.Context, sock string) (net.Conn, error) {
	var last error
	for i := 0; i < 100; i++ {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
		if err == nil {
			return conn, nil
		}
		last = err
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("connecting to the session helper's socket: %w", last)
}

// writeHelperPlist renders the LaunchAgent the bootstrap consumes. RunAtLoad
// starts the helper the moment the job lands; with KeepAlive off the job ends
// when the wire closes, and a reconnect bootstraps a fresh one. The helper's
// own diagnostics land in the work directory, which is cleaned up with the
// job, so a failing backend is not swallowed in silence.
func writeHelperPlist(path, label, exe string, opts Options, sock string, target DelegateTarget) error {
	argv := append([]string{exe}, opts.HelperCommand...)
	env := map[string]string{
		EnvHelperSocket: sock,
		"HOME":          target.Home,
		"USER":          target.User,
		"LOGNAME":       target.User,
	}
	for _, entry := range target.Env {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}

	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>Label</key>\n  <string>" + xmlEscape(label) + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range argv {
		b.WriteString("    <string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	for _, key := range sortedKeys(env) {
		b.WriteString("    <key>" + xmlEscape(key) + "</key>\n    <string>" + xmlEscape(env[key]) + "</string>\n")
	}
	b.WriteString("  </dict>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <false/>\n")
	b.WriteString("  <key>StandardErrorPath</key>\n  <string>" + xmlEscape(filepath.Join(filepath.Dir(path), "helper.err")) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing the session helper's job: %w", err)
	}
	return nil
}

// xmlEscape renders a string as plist-safe XML text.
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sortedKeys returns a map's keys in a stable order, so a rendered job does
// not churn between restarts.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
