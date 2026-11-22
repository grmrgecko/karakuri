//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grmrgecko/karakuri/automation/desktopenv"
)

type unixRequest struct {
	Args      []string
	Directory string
	Timeout   time.Duration
}
type unixResponse struct {
	Stdout, Stderr string
	Code           int
}

func residentDirectory() (string, error) {
	dir := filepath.Join("/tmp", "karakuri-auto-"+strconv.Itoa(os.Geteuid()))
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return "", fmt.Errorf("resident directory must be owned by this user with mode 0700: %s", dir)
	}
	return dir, nil
}
func unixResidentSocket() (string, error) {
	dir, err := residentDirectory()
	return filepath.Join(dir, "control.sock"), err
}
func platformPreflight(ctx context.Context, args []string) (bool, error) {
	if args[0] == "resident" {
		if len(args) != 2 {
			return true, fmt.Errorf("usage: resident install|uninstall|serve")
		}
		switch args[1] {
		case "serve":
			return true, serveUnixResident(context.Background())
		case "install":
			return true, installUnixResident(ctx, false)
		case "uninstall":
			return true, installUnixResident(ctx, true)
		}
		return true, fmt.Errorf("unknown resident action %q", args[1])
	}
	// Attach to the user's own graphical session when this process was started
	// away from one — a display-less SSH login, cron, or a service. This is the
	// no-install path to the desktop: the session's environment is scraped from
	// the user's own running processes and adopted here, so the command drives
	// the real desktop in this process. A resident already running is preferred,
	// since it holds the clipboard selection and portal consent between commands;
	// without one, the attach stands on its own. Root and macOS reach the desktop
	// their own ways (logind delegation, a launchd helper), and the session verbs
	// and hidden helpers never touch a display, so none of them attach here.
	if drivesDesktop(args[0]) && os.Geteuid() != 0 && runtime.GOOS != "darwin" {
		if desktopenv.Attached() {
			// A live display in our own environment means we are in the session
			// already and drive it here, whether that is a desktop terminal or an
			// SSH login that carried one.
			return false, nil
		}
		// A resident that is running, or installed and startable, is preferred
		// over a fresh attach: it holds the clipboard selection and portal consent
		// across commands, which a one-shot process cannot. Forwarding starts an
		// installed-but-idle resident, which is what keeps a deliberately installed
		// one working on a compositor that does not replay XDG autostart, such as
		// Hyprland. `--local` forces the in-process attach instead.
		if os.Getenv("KARAKURI_AUTO_LOCAL") != "1" && (residentLive() || residentInstalled()) {
			return true, forwardUnixResident(ctx, args)
		}
		if res, ok := desktopenv.Discover(ctx, os.Geteuid()); ok {
			applyDesktopEnv(res)
			return false, nil
		}
		// No session found; the fall-through reaches the resident forwarding,
		// which reports how to enable access when nothing answers there either.
	}

	if os.Getenv("KARAKURI_AUTO_LOCAL") == "1" {
		return false, nil
	}
	if runsInProcess(args[0]) {
		return false, nil
	}
	if os.Geteuid() == 0 {
		return false, nil
	}
	if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == "" {
		return false, nil
	}
	if runtime.GOOS == "darwin" && args[0] != "run" && args[0] != "__resident-stop" {
		return false, nil
	}
	return true, forwardUnixResident(ctx, args)
}

// drivesDesktop reports whether a command needs the graphical session, so a
// detached login can decide whether to attach to one. The commands that run in
// process never touch a display, and resident management is its own path.
// Everything else, such as typing, capture, windows, and launching, needs one.
func drivesDesktop(cmd string) bool {
	return !runsInProcess(cmd) && cmd != "resident" && cmd != "__resident-stop"
}

// runsInProcess reports whether a command always runs in this process and is
// never forwarded to the resident. The session verbs run against logind, the
// hidden helpers serve a session themselves, and the rest need no desktop.
func runsInProcess(cmd string) bool {
	switch cmd {
	case "version", "image-match", "sleep", "sessions", "lock", "unlock", "login", "logout", "activate",
		"greeter", "serve-helper", "secure-input":
		return true
	}
	return false
}

// residentInstalled reports whether this user has installed a resident copy that
// forwarding could start. It mirrors the path forwardUnixResident bootstraps
// from, so an install that a compositor did not autostart is still honored rather
// than bypassed by a fresh attach.
func residentInstalled() bool {
	config, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(config, "karakuri-auto", "resident"))
	return err == nil && info.Mode().IsRegular()
}

// residentLive reports whether a resident is already listening for this user, so
// a command reuses its long-lived session instead of attaching a fresh one.
func residentLive() bool {
	socket, err := unixResidentSocket()
	if err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// applyDesktopEnv adopts a discovered session in this process: it drops the SSH
// variables so a launched application does not inherit the forwarding session,
// then sets the display, authority, runtime, and bus variables that place the
// process in the desktop. It is safe because this is a one-shot command that
// exits when it returns, never a long-lived server whose goroutines read the
// environment as it changes.
func applyDesktopEnv(res desktopenv.Result) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_TTY", "SSH_CLIENT"} {
		_ = os.Unsetenv(key)
	}
	// Clear any session variable the discovered session does not define before
	// adopting the ones it does. A forwarded DISPLAY from `ssh -X`, or a socket
	// left over from an ended session, would otherwise survive into this process
	// and a program it launches — pointing an X client at the operator's
	// forwarded server rather than the user's own desktop. LANG is the one key
	// that names no session: the login's locale stands when the session's
	// processes carry none.
	for _, key := range desktopenv.Keys {
		if _, ok := res.Env[key]; !ok && key != "LANG" {
			_ = os.Unsetenv(key)
		}
	}
	for key, value := range res.Env {
		_ = os.Setenv(key, value)
	}
}
func forwardUnixResident(ctx context.Context, args []string) error {
	path, err := unixResidentSocket()
	if err != nil {
		return err
	}
	var dial net.Dialer
	c, err := dial.DialContext(ctx, "unix", path)
	if err != nil && runtime.GOOS != "darwin" && len(args) > 0 && args[0] != "__resident-stop" {
		config, configErr := os.UserConfigDir()
		exe := filepath.Join(config, "karakuri-auto", "resident")
		if info, statErr := os.Stat(exe); configErr == nil && statErr == nil && info.Mode().IsRegular() {
			if startErr := startUnixDesktop(ctx, exe); startErr != nil {
				return startErr
			}
			c, err = dial.DialContext(ctx, "unix", path)
		}
	}
	if err != nil {
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("no desktop resident is running; run karakuri-auto resident install as the desktop user to launch applications over SSH: %w", err)
		}
		return fmt.Errorf("no graphical session was found to attach to, and no desktop resident is running; log in at the desktop, or run karakuri-auto resident install as the desktop user: %w", err)
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	if d, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(d)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	timeout := 30 * time.Minute
	if d, ok := ctx.Deadline(); ok {
		timeout = time.Until(d)
	}
	if err = json.NewEncoder(c).Encode(unixRequest{args, cwd, timeout}); err != nil {
		return err
	}
	var response unixResponse
	if err = json.NewDecoder(io.LimitReader(c, 50<<20)).Decode(&response); err != nil {
		return fmt.Errorf("reading resident reply (command was not retried): %w", err)
	}
	fmt.Fprint(os.Stdout, response.Stdout)
	fmt.Fprint(os.Stderr, response.Stderr)
	if response.Code != 0 {
		return commandExit{response.Code}
	}
	return nil
}
func serveUnixResident(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go watchDesktopSession(ctx, cancel)
	path, err := unixResidentSocket()
	if err != nil {
		return err
	}
	// A failed connection does not authorize deleting a live listener's socket.
	// flock holds ownership across stale-socket cleanup and listener creation.
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(path), "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for attempt := 0; ; attempt++ {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if attempt == 20 || (!errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN)) {
			return fmt.Errorf("resident already running: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	backendPath, closeBackend, err := startResidentBackend(ctx, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer closeBackend()
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var mu sync.Mutex
	active := 0
	stopping := false
	var running sync.WaitGroup
	defer running.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			mu.Lock()
			done := stopping
			mu.Unlock()
			if done || ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		if active >= 16 || stopping {
			mu.Unlock()
			_ = connection.Close()
			continue
		}
		active++
		mu.Unlock()
		running.Add(1)
		go func() {
			defer running.Done()
			defer connection.Close()
			defer func() { mu.Lock(); active--; mu.Unlock() }()
			_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
			var req unixRequest
			if json.NewDecoder(io.LimitReader(connection, 1<<20)).Decode(&req) != nil {
				return
			}
			response := unixResponse{}
			if len(req.Args) == 0 || req.Args[0] == "resident" {
				response.Code = 2
				response.Stderr = "invalid resident request\n"
			} else if req.Args[0] == "__resident-stop" {
				mu.Lock()
				if active > 1 {
					response.Code = 1
					response.Stderr = "resident has running commands; retry after they finish\n"
				} else {
					stopping = true
				}
				done := stopping
				mu.Unlock()
				if done {
					_ = listener.Close()
				}
			} else {
				timeout := req.Timeout
				if timeout <= 0 || timeout > 30*time.Minute {
					timeout = 30 * time.Minute
				}
				callCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				exe, err := os.Executable()
				if err != nil {
					return
				}
				cmd := exec.CommandContext(callCtx, exe, req.Args...)
				cmd.Dir = req.Directory
				cmd.Env = append(os.Environ(), "KARAKURI_AUTO_LOCAL=1", residentBackendEnv+"="+backendPath)
				cmd.WaitDelay = 2 * time.Second
				out, errout := &limitedOutput{}, &limitedOutput{}
				cmd.Stdout = out
				cmd.Stderr = errout
				err = cmd.Run()
				response = unixResponse{out.String(), errout.String(), exitStatus(err)}
				if err != nil && response.Stderr == "" {
					response.Stderr = err.Error() + "\n"
				}
			}
			_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_ = json.NewEncoder(connection).Encode(response)
		}()
	}
}
func installUnixResident(ctx context.Context, remove bool) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("install the resident as the desktop user, not root")
	}
	socket, err := unixResidentSocket()
	if err != nil {
		return err
	}
	if c, e := net.DialTimeout("unix", socket, time.Second); e == nil {
		_ = c.Close()
		time.Sleep(50 * time.Millisecond)
		if err = forwardUnixResident(ctx, []string{"__resident-stop"}); err != nil {
			return err
		}
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	copyPath := filepath.Join(config, "karakuri-auto", "resident")
	if remove {
		if err := configureUnixStartup(ctx, "", true); err != nil {
			return err
		}
		// The copy is what residentInstalled and forwarding bootstrap from, so
		// leaving it behind would keep every SSH command starting a resident the
		// user just asked to be rid of, instead of attaching in process.
		if runtime.GOOS != "darwin" {
			for _, path := range []string{copyPath, copyPath + ".new"} {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			fmt.Printf("resident copy removed from %s\n", copyPath)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(copyPath), 0700); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		copyPath = exe
	} else {
		data, err := os.ReadFile(exe)
		if err != nil {
			return err
		}
		staged := copyPath + ".new"
		if err = os.WriteFile(staged, data, 0700); err != nil {
			return err
		}
		if err = os.Rename(staged, copyPath); err != nil {
			return err
		}
	}
	if err = configureUnixStartup(ctx, copyPath, false); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		if err = startUnixDesktop(ctx, copyPath); err != nil {
			return err
		}
	}
	if err = waitUnixResident(ctx, socket); err != nil {
		return err
	}
	fmt.Printf("resident installed at %s\n", copyPath)
	return nil
}
func startUnixDesktop(ctx context.Context, exe string) error {
	env, err := desktopEnvironment(ctx)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "resident", "serve")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	log, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "resident.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	socket, err := unixResidentSocket()
	if err != nil {
		return err
	}
	return waitUnixResident(ctx, socket)
}
func waitUnixResident(ctx context.Context, socket string) error {
	for i := 0; i < 50; i++ {
		if c, e := net.DialTimeout("unix", socket, 100*time.Millisecond); e == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("resident did not start; inspect karakuri-auto/resident.log in the user configuration directory")
}

// mergeDesktopEnvironment builds a resident's environment from this login's
// and the desktop's. session names the variables that place a process in a
// graphical session; the login's value for one never passes through, because
// a forwarded DISPLAY or the SSH login's own XDG_SESSION_ID would send the
// resident somewhere other than the desktop it serves.
func mergeDesktopEnvironment(values map[string]string, session map[string]bool) []string {
	env := make([]string, 0)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "SSH_CONNECTION" || key == "SSH_TTY" || key == "SSH_CLIENT" || session[key] {
			continue
		}
		if _, ok := values[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func prepareBackgroundProcess(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
