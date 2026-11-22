// Command karakuri-auto drives the local desktop through the automation module.
// It also reports the selected backends so an operator can diagnose a host
// without an agent, server, or flow between the caller and the platform.
//
//	karakuri-auto describe                     show the selected backend per capability
//	karakuri-auto version                      show the build version
//	karakuri-auto sessions                     list login sessions
//	karakuri-auto lock                         lock the active session
//	karakuri-auto activate <user>              switch to a user's session
//	karakuri-auto greeter                      show the login window
//	karakuri-auto logout <user>                end a user's session
//	karakuri-auto unlock <user> [password]     unlock a session with credentials
//	karakuri-auto login <user> [password]      switch to and unlock a session
//	karakuri-auto type <text>                  type text into the focused control
//	karakuri-auto press <keys>                 press a key combination (ctrl+alt+t)
//	karakuri-auto windows                      list the session's windows
//	karakuri-auto notify <title> <body>        post a notification
//	karakuri-auto screenshot [options] [file]            capture a PNG (default karakuri-auto.png)
//	karakuri-auto serve-helper                 hidden: serve one delegated desktop session
//	karakuri-auto secure-input                 hidden: type one credential for a SYSTEM parent
//
// A password argument sits in the process list, so the tool also takes it on
// standard input when the argument is omitted or "-". Typing, windows, and the
// typed unlock need the process trusted for Accessibility (System Settings >
// Privacy & Security > Accessibility): the grant follows this binary's path.
// A root run on Linux or macOS delegates the desktop to a helper owned by the
// console session's user, the same decision a root agent makes. On Windows
// the credentialed unlock runs from a SYSTEM service, which starts this same
// binary once inside the locked session to type there.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/internal/version"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--local" {
		_ = os.Setenv("KARAKURI_AUTO_LOCAL", "1")
		args = args[1:]
	}

	opts := automationOptions()

	// Installing the credential provider is opt-in
	// (KARAKURI_INSTALL_UNLOCK_PROVIDER=1). It is a no-op except on Windows,
	// where it writes the logon-screen credential provider DLL beside the
	// binary — the same install the agent's startup runs.
	if os.Getenv("KARAKURI_INSTALL_UNLOCK_PROVIDER") == "1" {
		if status, err := automation.EnsureUnlockProvider(); err != nil {
			fmt.Println("credential provider install failed:", err)
		} else if status != "" {
			fmt.Println(status)
		}
	}

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd, cmdArgs := args[0], args[1:]
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		usage()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if handled, err := platformPreflight(ctx, args); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitStatus(err))
		}
		return
	}
	switch cmd {
	case "run", "sleep", "script":
		var err error
		switch cmd {
		case "run":
			err = runProcess(ctx, cmdArgs)
		case "sleep":
			err = sleepCommand(ctx, cmdArgs)
		case "script":
			err = scriptCommand(ctx, cmdArgs)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitStatus(err))
		}
		return
	case "image-match":
		if err := imageMatchCommand(ctx, cmdArgs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitStatus(err))
		}
		return
	case "version":
		fmt.Printf("%s-auto %s\n", version.Name, version.String())
		return
	case "serve-helper":
		// The hidden half: serves one session for a privileged parent until
		// the wire closes. No deadline, the parent's calls pace the work.
		if err := automation.ServeHelperCommand(context.Background(), opts); err != nil && err != context.Canceled {
			fmt.Fprintln(os.Stderr, "serve-helper:", err)
			os.Exit(1)
		}
		return
	case "secure-input":
		// The Windows hidden half: one request on standard input, typed into
		// the console this process was started in.
		if err := automation.ServeSecureInputCommand(context.Background(), opts); err != nil {
			fmt.Fprintln(os.Stderr, "secure-input:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(ctx, opts, cmd, cmdArgs); err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		os.Exit(exitStatus(err))
	}
}

func automationOptions() automation.Options {
	opts := automation.Options{AppID: "karakuri-auto"}
	// Persist portal restore tokens beside the user's config, the way an agent
	// persists them under --dir, so consent is asked once per host rather than
	// once per run.
	if dir, err := os.UserConfigDir(); err == nil {
		opts.TokenStore = automation.FileTokenStore(filepath.Join(dir, "karakuri-auto"))
	}
	// Backend diagnostics go where the command's output already is, so a
	// helper's report of where it typed is visible on success and failure.
	opts.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	// The helper argvs are opt-ins for the processes that re-execute this
	// binary: delegation on Linux and macOS, the typed unlock on Windows.
	opts.HelperCommand = []string{"serve-helper"}
	opts.SecureInputCommand = []string{"secure-input"}

	return opts
}

// run composes the session once and dispatches one subcommand against it.
func run(ctx context.Context, opts automation.Options, cmd string, args []string) error {
	sess, err := commandSession(ctx, opts)
	if err != nil {
		return err
	}
	defer sess.Close()
	env := sess.Describe()
	if env.Delegate != "" {
		fmt.Fprintf(os.Stderr, "delegated to %s\n", env.Delegate)
	}

	switch cmd {
	case "describe":
		fmt.Printf("os=%s display=%s compositor=%s\n", env.OS, env.Display, env.Compositor)
		for _, name := range sortedBackends(env) {
			fmt.Printf("  %-9s %s\n", name, env.Backends[name])
		}
		return nil

	case "sessions":
		list, err := sess.Sessions.List(ctx)
		if err != nil {
			return err
		}
		for _, s := range list {
			state := "online"
			if s.Active {
				state = "active"
			}
			lock := ""
			if s.LockKnown && s.Locked {
				lock = " locked"
			}
			fmt.Printf("id=%s uid=%d user=%s class=%s %s%s\n", s.ID, s.UID, s.User, s.Class, state, lock)
		}
		if len(list) == 0 {
			fmt.Println("no sessions reported")
		}
		return nil

	case "lock":
		target, err := activeSession(ctx, sess)
		if err != nil {
			return err
		}
		return sess.Sessions.Act(ctx, target, automation.SessionLock)

	case "activate":
		target, err := userSession(ctx, sess, arg(cmd, args, 0))
		if err != nil {
			return err
		}
		return sess.Sessions.Act(ctx, target, automation.SessionActivate)

	case "greeter":
		return sess.Sessions.Act(ctx, automation.SessionInfo{}, automation.SessionGreeter)

	case "logout":
		target, err := userSession(ctx, sess, arg(cmd, args, 0))
		if err != nil {
			return err
		}
		return sess.Sessions.Act(ctx, target, automation.SessionLogout)

	case "unlock", "login":
		action := automation.SessionUnlock
		if cmd == "login" {
			action = automation.SessionLogin
		}
		user := arg(cmd, args, 0)
		password, err := readPassword(cmd, args, 1)
		if err != nil {
			return err
		}
		// A target that matches nothing is handed to the backend naming just
		// the account. Backends that can create a fresh login do so; Linux
		// reports that logind needs an existing graphical session.
		target, err := userSession(ctx, sess, user)
		if err != nil && cmd != "login" {
			return err
		}
		err = sess.Sessions.Login(ctx, automation.LoginRequest{
			User:   user,
			Secret: password,
			Target: target,
			Action: action,
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s of %s (session %s) done\n", cmd, user, target.ID)
		return nil

	case "type":
		text := strings.Join(args, " ")
		if text == "" {
			return fmt.Errorf("no text to type")
		}
		return sess.Keyboard.TypeText(ctx, automation.RealClock{}, text, 12*time.Millisecond)

	case "press":
		stroke, err := automation.ParseKeyStroke(arg(cmd, args, 0))
		if err != nil {
			return err
		}
		return sess.Keyboard.PressKeys(ctx, stroke)

	case "windows":
		windows, err := sess.Window.List(ctx)
		if err != nil {
			return err
		}
		for _, w := range windows {
			mark := ""
			if w.Active {
				mark = " *"
			}
			fmt.Printf("id=%s pid=%d class=%s title=%q x=%d y=%d width=%d height=%d%s\n",
				w.ID, w.PID, w.Class, w.Title, w.X, w.Y, w.Width, w.Height, windowDetails(w)+mark)
		}
		if len(windows) == 0 {
			fmt.Println("no windows reported")
		}
		return nil

	case "window":
		return windowCommand(ctx, sess, args)
	case "drag":
		return dragCommand(ctx, sess, args)

	case "notify":
		if len(args) < 2 {
			return fmt.Errorf("usage: notify <title> <body>")
		}
		return sess.Notifier.Notify(ctx, automation.Notification{Title: args[0], Message: args[1]})

	case "copy":
		text := strings.Join(args, " ")
		if text == "" {
			return fmt.Errorf("no text to copy")
		}
		if err := sess.Clipboard.Write(ctx, autoapi.SelectionClipboard, text); err != nil {
			return err
		}
		// A selection owned in this process disappears when the process exits.
		// Say so rather than exiting as if the clipboard now holds the text: a
		// one-shot command has no way to keep owning it, so a persistent holder
		// (an installed resident, or a running clipboard manager) is what a later
		// paste depends on.
		if backend := env.Backends["clipboard"]; backend == "wayland-data-control" || backend == "x11-native" {
			fmt.Fprintf(os.Stderr, "note: this %s clipboard is held only while a process owns it; without a running clipboard manager or karakuri-auto resident, the selection is dropped when this command exits\n", backend)
		}
		return nil

	case "paste":
		text, err := sess.Clipboard.Read(ctx, autoapi.SelectionClipboard)
		if err != nil {
			return err
		}
		fmt.Println(text)
		return nil

	case "clip":
		// A Wayland selection belongs to a live client, so a write followed by
		// a read must happen in one process to prove the round trip.
		text := strings.Join(args, " ")
		if text == "" {
			text = "karakuri-auto-roundtrip"
		}
		if err := sess.Clipboard.Write(ctx, autoapi.SelectionClipboard, text); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		got, err := sess.Clipboard.Read(ctx, autoapi.SelectionClipboard)
		if err != nil {
			return fmt.Errorf("read back: %w", err)
		}
		if got != text {
			return fmt.Errorf("round trip mismatch: wrote %q, read %q", text, got)
		}
		fmt.Println("round trip ok:", got)
		return nil

	case "move":
		if len(args) < 2 {
			return fmt.Errorf("usage: move <x> <y>")
		}
		x, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("x: %w", err)
		}
		y, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("y: %w", err)
		}
		return sess.Mouse.Move(ctx, x, y, false)

	case "click":
		return clickCommand(ctx, sess, args)
	case "wait":
		if len(args) > 0 && args[0] == "state" {
			return waitState(ctx, sess, args)
		}
		return waitWindow(ctx, sess, args)
	case "assert":
		return waitState(ctx, sess, args)
	case "snapshot":
		return snapshotCommand(ctx, sess, args)
	case "controls", "menu", "elements", "display", "dialog":
		return desktopCommand(ctx, sess, cmd, args)

	case "screenshot":
		return takeScreenshot(ctx, sess, args)
	}
	usage()
	return fmt.Errorf("unknown command %q", cmd)
}

// activeSession picks the session a person is sitting at, for the verbs that
// act on the console rather than on a named user.
func activeSession(ctx context.Context, sess *automation.Session) (automation.SessionInfo, error) {
	list, err := sess.Sessions.List(ctx)
	if err != nil {
		return automation.SessionInfo{}, err
	}
	for _, s := range list {
		if s.Active && s.Graphical() {
			return s, nil
		}
	}
	return automation.SessionInfo{}, fmt.Errorf("no active graphical session; run `karakuri-auto sessions` to see what the host reports")
}

// userSession finds the named user's session, by name or by uid, the way the
// session node resolves one.
func userSession(ctx context.Context, sess *automation.Session, who string) (automation.SessionInfo, error) {
	if who == "" {
		return automation.SessionInfo{}, fmt.Errorf("no user named")
	}
	list, err := sess.Sessions.List(ctx)
	if err != nil {
		return automation.SessionInfo{}, err
	}
	byUID := -1
	if uid, err := strconv.Atoi(who); err == nil {
		byUID = uid
	}
	var best automation.SessionInfo
	found := false
	for _, s := range list {
		match := (byUID >= 0 && s.UID == byUID) ||
			strings.EqualFold(s.User, who) || strings.EqualFold(autoapi.AccountName(s.User), who)
		if !match {
			continue
		}
		// A user signed in at a desktop also holds SSH and service-manager
		// sessions; rank a graphical, active one first so a lock or unlock
		// lands on the screen rather than on a terminal, the way the Session
		// node's own resolver does.
		if !found || betterSession(s, best) {
			best, found = s, true
		}
	}
	if !found {
		return automation.SessionInfo{}, fmt.Errorf("%s has no session on this host", who)
	}
	return best, nil
}

// betterSession ranks two candidate sessions the way the Session node does: a
// graphical session beats a terminal, an active one beats an idle one, then the
// most recently started wins.
func betterSession(a, b automation.SessionInfo) bool {
	if a.Graphical() != b.Graphical() {
		return a.Graphical()
	}
	if a.Active != b.Active {
		return a.Active
	}
	return a.Since.After(b.Since)
}

// readPassword takes the password from argv, or from one line of standard
// input when the argument is missing or "-", so the process list can be kept
// clear of it where that matters.
func readPassword(cmd string, args []string, at int) (string, error) {
	if len(args) > at && args[at] != "-" {
		return args[at], nil
	}
	fmt.Printf("password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading the password from standard input: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", fmt.Errorf("no password given")
	}
	return password, nil
}

// arg returns the subcommand argument at a position, with a usage note when it
// is missing.
func arg(cmd string, args []string, at int) string {
	if len(args) > at {
		return args[at]
	}
	return ""
}

// sortedBackends returns the Describe backend names in a stable order.
func sortedBackends(env automation.Environment) []string {
	names := make([]string, 0, len(env.Backends))
	for name := range env.Backends {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// usage prints the command list the package comment documents.
func usage() {
	fmt.Fprint(os.Stderr, `usage: karakuri-auto <command> [args]

  describe                    what was detected, per capability
  version                     show the build version
  sessions                    list login sessions
  lock                        lock the active session
  activate <user>             switch to a user's session
  greeter                     show the login window
  logout <user>               end a user's session
  unlock <user> [password]    credentialed unlock (password on stdin if "-")
  login <user> [password]     switch-and-unlock
  type <text>                 type text where focus is
  press <keys>                press a key combination, e.g. ctrl+alt+t
  windows                     list windows (" *" marks the focused one)
  notify <title> <body>       post a notification
  window <action> <match>     activate/minimize/maximize/close/restore a window
  window place TARGET X Y WIDTH HEIGHT  request window bounds
  drag X1 Y1 X2 Y2 [options]  drag while holding a button
  copy <text>                 write the clipboard
  paste                       read the clipboard
  move <x> <y>                move the pointer
  click [x y]                 left-click, optionally moving first
  click --window match --text label  click a child control by label
  controls <window>           list child control text, IDs, classes, and rectangles
  menu list <window>          list the native menu tree
  menu invoke <window> <path|id>  invoke a menu item
  elements <window>           list UI Automation elements
  dialog list                 list modal dialogs and their control text
  display [set WIDTHxHEIGHTxDEPTH]  inspect or change the primary display
  run <exe> [args] [--wait]    launch an application in the desktop session
  wait window <match> [--timeout seconds] [--gone]
  snapshot <window> [--all] [--enabled]  inspect visible accessibility elements as JSON
  wait state --window <match> [--text text] [--role role] [--enabled] [--gone]
             [--stable seconds] [--region x,y,width,height] [--timeout seconds]
             [--evidence directory]
  assert state <options>     wait for a state; save evidence on failure
  sleep <seconds>             pause, accepting fractional seconds
  script <file> [--log file]  run a command file, stopping on the first failure
  image-match IMAGE REFERENCE [options]  find same-scale image regions by percentage
  resident install|uninstall|serve  manage desktop session forwarding
  screenshot [options] [file] capture a PNG (default karakuri-auto.png)
    --region x,y,width,height  capture part of a screen
    --display index            select a screen (default 0)
    --window match             capture a window by ID, title, or class
    --active-window            capture the foreground window

Desktop access:
  Over SSH as the desktop's own user, Linux and FreeBSD attach to that session
  automatically: the display, authority, and bus are read from the user's own
  processes and the command drives the desktop in place. As root, the logged-in
  user's session is delegated to. No resident and no install are needed. macOS
  runs GUI commands through a temporary helper the same way; grant this executable
  Accessibility and Screen Recording in System Settings > Privacy & Security and
  keep its path and signing identity stable across updates.

  The resident is optional. It keeps a Wayland clipboard selection alive after a
  command exits and launches applications with run on macOS. On Windows, an SSH
  login that is not the console user forwards to a resident; install it with
  karakuri-auto resident install as the desktop user and reinstall after updating
  the binary.
`)
}
