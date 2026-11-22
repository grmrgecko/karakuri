//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/idleparse"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// Linux, FreeBSD, and the other Unixes reach the desktop through whatever tools
// the session has installed rather than through one API, so each function walks
// a list in preference order and reports what is missing when none is there.
// The agent inherits DISPLAY, WAYLAND_DISPLAY, and DBUS_SESSION_BUS_ADDRESS
// from the session that started it, which is what makes any of this work; an
// agent started by systemd --system has none of them and every tool here fails
// with its own complaint about not finding a display.

// volumeStep is how far one volume up or down moves, in percent. It matches
// what a keyboard's volume key does on GNOME and macOS.
const volumeStep = 5

// notifyDesktop shows a notification over the session bus. notify-send is not
// a fallback because it talks to the same service on the same bus: a session
// this call cannot reach is one the tool cannot reach either.
func notifyDesktop(ctx context.Context, n autoapi.Notification) error {
	return notifyOverBus(ctx, n)
}

// idleSeconds asks the session how long it has been untouched, trying the X
// server first, then GNOME's idle monitor, then logind — all direct calls now,
// with nothing to install. The sources disagree about what counts as idle: the
// screensaver extension and Mutter report time since the last input event,
// while logind's IdleSinceHint follows the session being marked idle, which on
// a locked screen is the lock rather than the last keypress. The source is
// reported alongside the number for that reason.
func idleSeconds(ctx context.Context, now time.Time) (float64, string, error) {
	wayland := strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""

	// On a real X11 session the MIT-SCREEN-SAVER extension is the native answer
	// and needs nothing installed, so it leads. It is skipped on Wayland: read
	// through XWayland it counts only input to X clients, so its clock keeps
	// climbing while the user works in Wayland-native apps — there the
	// compositor's own idle below is the honest source.
	if !wayland {
		if millis, err := x11IdleMillis(); err == nil {
			return idleparse.FromMillis(int64(millis)), "x11-screensaver", nil
		}
	}

	if millis, err := mutterIdleMillis(ctx); err == nil {
		return idleparse.FromMillis(int64(millis)), "mutter", nil
	}

	if idle, micros, err := logindIdleHint(ctx); err == nil {
		return logindIdleSeconds(idle, micros, now), "logind", nil
	}

	// Last resort on Wayland: XWayland's screensaver clock over-reports on a
	// Wayland-native desktop, but a number the operator can see is labelled and
	// better than a bare failure when the compositor offers no idle at all.
	if wayland {
		if millis, err := x11IdleMillis(); err == nil {
			return idleparse.FromMillis(int64(millis)), "x11-screensaver (xwayland)", nil
		}
	}

	return 0, "", fmt.Errorf("no idle source answered: this session has no X display, " +
		"no GNOME idle monitor, and no logind session")
}

// logindIdleSeconds converts IdleHint and IdleSinceHint into seconds of
// idleness. IdleSinceHint also moves when the hint clears, so a session that
// is not idle reports 0 whatever its stamp says.
func logindIdleSeconds(idle bool, micros uint64, now time.Time) float64 {
	if !idle || micros == 0 {
		return 0
	}
	seconds := now.Sub(time.UnixMicro(int64(micros))).Seconds()
	if seconds < 0 {
		seconds = 0
	}
	return seconds
}

// mediaControl drives playback over MPRIS and the volume through whichever
// mixer the host runs. playerctl is not a fallback for the playback half: it
// speaks the same MPRIS interface on the same bus, so there is no player it
// reaches that the direct call cannot.
func mediaControl(ctx context.Context, action, player string, volume float64) error {
	switch action {
	case mediaVolumeSet, mediaVolumeUp, mediaVolumeDown, mediaMute, mediaUnmute, mediaMuteToggle:
		return unixVolume(ctx, action, volume)
	}
	return mprisPlayback(ctx, action, player)
}

// unixVolume changes the default sink, speaking the PulseAudio protocol
// directly first — which PipeWire serves too — then FreeBSD's kernel mixer on
// its own ioctl, and only then the mixer tools: wpctl reaches PipeWire on its
// own protocol, then amixer for bare ALSA, and finally FreeBSD's mixer tool.
// pactl stays in the chain for the exotic server strings the C client
// resolves.
func unixVolume(ctx context.Context, action string, volume float64) error {
	if err := pulseVolume(ctx, action, volume); err == nil || !errors.Is(err, errNoPulse) {
		return err
	}

	if handled, err := ossVolume(action, volume); handled {
		return err
	}

	if handled, err := alsaVolume(action, volume); handled {
		return err
	}

	if tool, ok := toolsh.ToolPath("wpctl"); ok {
		const sink = "@DEFAULT_AUDIO_SINK@"
		switch action {
		case mediaVolumeSet:
			// wpctl takes a fraction rather than a percentage.
			_, err := toolsh.RunTool(ctx, tool, "set-volume", sink, strconv.FormatFloat(volume/100, 'f', 2, 64))
			return err
		case mediaVolumeUp:
			_, err := toolsh.RunTool(ctx, tool, "set-volume", sink, fmt.Sprintf("%d%%+", volumeStep))
			return err
		case mediaVolumeDown:
			_, err := toolsh.RunTool(ctx, tool, "set-volume", sink, fmt.Sprintf("%d%%-", volumeStep))
			return err
		case mediaMute:
			_, err := toolsh.RunTool(ctx, tool, "set-mute", sink, "1")
			return err
		case mediaUnmute:
			_, err := toolsh.RunTool(ctx, tool, "set-mute", sink, "0")
			return err
		case mediaMuteToggle:
			_, err := toolsh.RunTool(ctx, tool, "set-mute", sink, "toggle")
			return err
		}
	}

	if tool, ok := toolsh.ToolPath("pactl"); ok {
		const sink = "@DEFAULT_SINK@"
		switch action {
		case mediaVolumeSet:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-volume", sink, strconv.FormatFloat(volume, 'f', 0, 64)+"%")
			return err
		case mediaVolumeUp:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-volume", sink, fmt.Sprintf("+%d%%", volumeStep))
			return err
		case mediaVolumeDown:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-volume", sink, fmt.Sprintf("-%d%%", volumeStep))
			return err
		case mediaMute:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-mute", sink, "1")
			return err
		case mediaUnmute:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-mute", sink, "0")
			return err
		case mediaMuteToggle:
			_, err := toolsh.RunTool(ctx, tool, "set-sink-mute", sink, "toggle")
			return err
		}
	}

	if tool, ok := toolsh.ToolPath("amixer"); ok {
		switch action {
		case mediaVolumeSet:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", strconv.FormatFloat(volume, 'f', 0, 64)+"%")
			return err
		case mediaVolumeUp:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", fmt.Sprintf("%d%%+", volumeStep))
			return err
		case mediaVolumeDown:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", fmt.Sprintf("%d%%-", volumeStep))
			return err
		case mediaMute:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", "mute")
			return err
		case mediaUnmute:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", "unmute")
			return err
		case mediaMuteToggle:
			_, err := toolsh.RunTool(ctx, tool, "-q", "set", "Master", "toggle")
			return err
		}
	}

	// FreeBSD's mixer has no mute of its own; muting there means zero.
	if tool, ok := toolsh.ToolPath("mixer"); ok {
		switch action {
		case mediaVolumeSet:
			_, err := toolsh.RunTool(ctx, tool, "vol", strconv.FormatFloat(volume, 'f', 0, 64))
			return err
		case mediaVolumeUp:
			_, err := toolsh.RunTool(ctx, tool, "vol", fmt.Sprintf("+%d", volumeStep))
			return err
		case mediaVolumeDown:
			_, err := toolsh.RunTool(ctx, tool, "vol", fmt.Sprintf("-%d", volumeStep))
			return err
		case mediaMute:
			_, err := toolsh.RunTool(ctx, tool, "vol", "0")
			return err
		}
		return fmt.Errorf("mixer cannot %s; set the volume instead", action)
	}

	return autoapi.MissingTool("change the volume", "wpctl", "pactl", "amixer", "mixer")
}

// clipboardRead returns the selection's current text.
func clipboardRead(ctx context.Context, selection string) (string, error) {
	if tool, ok := toolsh.ToolPath("wl-paste"); ok {
		args := []string{"--no-newline"}
		if selection == primarySelection {
			args = append(args, "--primary")
		}
		return toolsh.RunTool(ctx, tool, args...)
	}
	if tool, ok := toolsh.ToolPath("xclip"); ok {
		return toolsh.RunTool(ctx, tool, "-selection", xSelection(selection), "-out")
	}
	if tool, ok := toolsh.ToolPath("xsel"); ok {
		return toolsh.RunTool(ctx, tool, "--"+xSelection(selection), "--output")
	}
	return "", autoapi.MissingTool("read the clipboard", "wl-paste", "xclip", "xsel")
}

// clipboardWrite puts text on the selection. The X11 tools own the selection
// for as long as they run, so they are detached rather than waited for: xclip
// forks on its own, and a wait would block until something else takes the
// selection away.
func clipboardWrite(ctx context.Context, selection, text string) error {
	if tool, ok := toolsh.ToolPath("wl-copy"); ok {
		args := []string{}
		if selection == primarySelection {
			args = append(args, "--primary")
		}
		_, err := toolsh.RunToolInput(ctx, text, tool, args...)
		return err
	}
	if tool, ok := toolsh.ToolPath("xclip"); ok {
		_, err := toolsh.RunToolInput(ctx, text, tool, "-selection", xSelection(selection), "-in")
		return err
	}
	if tool, ok := toolsh.ToolPath("xsel"); ok {
		_, err := toolsh.RunToolInput(ctx, text, tool, "--"+xSelection(selection), "--input")
		return err
	}
	return autoapi.MissingTool("write the clipboard", "wl-copy", "xclip", "xsel")
}

// xSelection maps the selection onto what xclip and xsel call it.
func xSelection(selection string) string {
	if selection == primarySelection {
		return "primary"
	}
	return "clipboard"
}

// openURL hands a link to the desktop's default handler: the portal first
// because it needs nothing installed, then the scheme's registered .desktop
// entry read straight from the mimeapps lists — xdg-open's own resolution —
// with the tools last for the associations that lookup does not model.
func openURL(ctx context.Context, url string) error {
	if err := openURIOverPortal(ctx, url); err == nil {
		return nil
	}
	if err := openURLByHandler(url); err == nil {
		return nil
	}
	tool, ok := toolsh.ToolPath("xdg-open", "gio", "sensible-browser")
	if !ok {
		return autoapi.MissingTool("open a URL", "a desktop portal", "a mimeapps association", "xdg-open", "gio", "sensible-browser")
	}
	if strings.HasSuffix(tool, "gio") {
		return toolsh.StartDetached(tool, "open", url)
	}
	return toolsh.StartDetached(tool, url)
}

// openApp launches a program or a .desktop entry and leaves it running. A
// .desktop id is launched from the entry's own Exec line, a Terminal=true
// entry inside whichever emulator the host has; gtk-launch remains the
// fallback for the entries the parser declines.
func openApp(ctx context.Context, app string, args []string) error {
	if strings.HasSuffix(app, ".desktop") {
		id := strings.TrimSuffix(app, ".desktop")
		err := launchDesktopEntry(id, args)
		if err == nil {
			return nil
		}
		if errors.Is(err, errNeedsTerminal) {
			if termErr := launchTerminalEntry(id, args); termErr == nil {
				return nil
			}
		}
		if tool, ok := toolsh.ToolPath("gtk-launch"); ok {
			return toolsh.StartDetached(tool, append([]string{id}, args...)...)
		}
		if errors.Is(err, errNeedsTerminal) {
			return fmt.Errorf("%s runs in a terminal and no terminal emulator was found to hold it", app)
		}
		return err
	}
	path, ok := toolsh.ToolPath(app)
	if !ok {
		if !strings.ContainsRune(app, os.PathSeparator) {
			return fmt.Errorf("%s is not in the agent's PATH; use an absolute path or a .desktop id", app)
		}
		path = app
	}
	return toolsh.StartDetached(path, args...)
}

// powerControl performs a session or power action. logind is asked directly —
// the same D-Bus calls loginctl and systemctl make for these verbs, subject to
// the same polkit policy — and the tools remain only for hosts that do not run
// it: FreeBSD, and Linux without systemd.
func powerControl(ctx context.Context, action string) error {
	switch action {
	case powerLock:
		if err := logindSessionCall(ctx, "Lock"); err == nil {
			return nil
		}
		if err := screenSaverLock(ctx); err == nil {
			return nil
		}
		if tool, ok := toolsh.ToolPath("xdg-screensaver"); ok {
			_, err := toolsh.RunTool(ctx, tool, "lock")
			return err
		}
		return autoapi.MissingTool("lock the session", "logind", "a screensaver service", "xdg-screensaver")

	case powerSleep:
		if err := logindManagerCall(ctx, "Suspend"); err == nil {
			return nil
		}
		if handled, err := acpiSuspend(); handled {
			return err
		}
		if tool, ok := toolsh.ToolPath("zzz", "acpiconf"); ok {
			if strings.HasSuffix(tool, "acpiconf") {
				_, err := toolsh.RunTool(ctx, tool, "-s", "3")
				return err
			}
			_, err := toolsh.RunTool(ctx, tool)
			return err
		}
		return autoapi.MissingTool("suspend the host", "logind", "zzz", "acpiconf")

	case powerLogout:
		if err := logindSessionCall(ctx, "Terminate"); err == nil {
			return nil
		}
		if err := gnomeSessionLogout(ctx); err == nil {
			return nil
		}
		if tool, ok := toolsh.ToolPath("gnome-session-quit"); ok {
			_, err := toolsh.RunTool(ctx, tool, "--logout", "--no-prompt")
			return err
		}
		return autoapi.MissingTool("log the session out", "logind", "a session manager", "gnome-session-quit")

	case powerRestart:
		if err := logindManagerCall(ctx, "Reboot"); err == nil {
			return nil
		}
		if handled, err := initSignalPower(action); handled {
			return err
		}
		if tool, ok := toolsh.ToolPath("shutdown"); ok {
			_, err := toolsh.RunTool(ctx, tool, "-r", "now")
			return err
		}
		return autoapi.MissingTool("restart the host", "logind", "shutdown")

	case powerShutdown:
		if err := logindManagerCall(ctx, "PowerOff"); err == nil {
			return nil
		}
		if handled, err := initSignalPower(action); handled {
			return err
		}
		if tool, ok := toolsh.ToolPath("shutdown"); ok {
			// -p powers off on FreeBSD, where -h only halts.
			_, err := toolsh.RunTool(ctx, tool, "-p", "now")
			return err
		}
		return autoapi.MissingTool("shut the host down", "logind", "shutdown")
	}
	return fmt.Errorf("unknown power action %q", action)
}

// defaultSpeechWPM is the words per minute speech-dispatcher treats as its
// default pace, and the midpoint the rate is scaled around.
const defaultSpeechWPM = 175

// speakText reads text out through the session's speech service, on its own
// socket when the daemon is up. A silent socket starts the daemon the way the
// client library does — `speech-dispatcher --spawn` — and retries, which was
// spd-say's one remaining job; spd-say and espeak stay behind it for a host
// whose daemon will not start or does not exist.
func speakText(ctx context.Context, text, voice string, rate int, wait bool) error {
	if err := speakSSIP(ctx, text, voice, rate, wait); err == nil || !errors.Is(err, errNoSpeechd) {
		return err
	}

	// --spawn returns once the daemon forks, or refuses when the config
	// disables autospawn; either way the retry's answer is the real one.
	if daemon, ok := toolsh.ToolPath("speech-dispatcher"); ok {
		if _, spawnErr := toolsh.RunTool(ctx, daemon, "--spawn"); spawnErr == nil {
			if err := speakSSIP(ctx, text, voice, rate, wait); err == nil || !errors.Is(err, errNoSpeechd) {
				return err
			}
		}
	}

	if tool, ok := toolsh.ToolPath("spd-say"); ok {
		args := []string{}
		if wait {
			args = append(args, "-w")
		}
		if voice != "" {
			args = append(args, "-y", voice)
		}
		if rate > 0 {
			args = append(args, "-r", strconv.Itoa(speechDispatcherRate(rate)))
		}
		_, err := toolsh.RunTool(ctx, tool, append(args, "--", text)...)
		return err
	}
	if tool, ok := toolsh.ToolPath("espeak-ng", "espeak"); ok {
		args := []string{}
		if voice != "" {
			args = append(args, "-v", voice)
		}
		if rate > 0 {
			args = append(args, "-s", strconv.Itoa(rate))
		}
		args = append(args, "--", text)
		if !wait {
			return toolsh.StartDetached(tool, args...)
		}
		_, err := toolsh.RunTool(ctx, tool, args...)
		return err
	}
	return autoapi.MissingTool("speak", "spd-say", "espeak-ng", "espeak")
}

// speechDispatcherRate converts words per minute into speech-dispatcher's own
// scale, where zero is its default pace and the ends are -100 and 100. The
// caller works in words per minute because that is a rate that means the same
// on every platform; the conversion belongs at the edge that needs the other.
func speechDispatcherRate(wpm int) int {
	scaled := (wpm - defaultSpeechWPM) * 100 / defaultSpeechWPM
	if scaled > 100 {
		return 100
	}
	if scaled < -100 {
		return -100
	}
	return scaled
}
