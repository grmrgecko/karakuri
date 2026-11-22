package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	kservice "github.com/kardianos/service"
)

// systemUnitDir is where systemd reads units an administrator installs.
const systemUnitDir = "/etc/systemd/system"

// Timer is a systemd timer that runs one command of the installing binary as
// root on a schedule. It exists for work the service's own account must not be
// able to do, such as replacing the root-owned files of a system install.
type Timer struct {
	// Name is the unit name without its .service or .timer suffix.
	Name string
	// Description is what systemctl shows for both units.
	Description string
	// Arguments are appended to the executable path.
	Arguments []string
	// OnCalendar and RandomizedDelaySec are the timer's schedule.
	OnCalendar         string
	RandomizedDelaySec string
}

// TimersSupported reports whether this host runs systemd, the only service
// manager a Timer is written for.
func TimersSupported() bool {
	return kservice.Platform() == "linux-systemd"
}

// TimerInstalled reports whether the named timer's unit exists.
func TimerInstalled(name string) bool {
	_, err := os.Stat(filepath.Join(systemUnitDir, name+".timer"))
	return err == nil
}

// InstallTimer writes the timer and the oneshot unit it starts, and enables
// the timer. Reinstalling overwrites both, so it also repairs a changed
// binary path.
func InstallTimer(t Timer) error {
	if !TimersSupported() {
		return errors.New("timers need systemd")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	unit, timer := t.units(exe)
	if err := os.WriteFile(filepath.Join(systemUnitDir, t.Name+".service"), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(systemUnitDir, t.Name+".timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return systemctl("enable", "--now", t.Name+".timer")
}

// units renders the oneshot unit and the timer that starts it.
func (t Timer) units(exe string) (unit, timer string) {
	command := []string{unitQuote(exe)}
	for _, arg := range t.Arguments {
		command = append(command, unitQuote(arg))
	}
	unit = fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s
`, t.Description, strings.Join(command, " "))
	timer = fmt.Sprintf(`[Unit]
Description=%s

[Timer]
OnCalendar=%s
RandomizedDelaySec=%s
Persistent=true

[Install]
WantedBy=timers.target
`, t.Description, t.OnCalendar, t.RandomizedDelaySec)
	return unit, timer
}

// UninstallTimer disables and removes the timer and its unit. A timer that was
// never installed is not an error.
func UninstallTimer(name string) error {
	if !TimersSupported() || !TimerInstalled(name) {
		return nil
	}
	_ = systemctl("disable", "--now", name+".timer")
	for _, suffix := range []string{".timer", ".service"} {
		if err := os.Remove(filepath.Join(systemUnitDir, name+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return systemctl("daemon-reload")
}

// unitQuote quotes one ExecStart word when it holds a space or a quote.
func unitQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// systemctl runs one systemctl command, folding its output into the error.
func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
