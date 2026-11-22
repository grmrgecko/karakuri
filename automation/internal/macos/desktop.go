//go:build darwin

package macos

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

// macOS is driven natively where a public C interface exists — the clipboard
// through NSPasteboard, the volume through CoreAudio, idle time through the IO
// registry — and through osascript where the sanctioned route is AppleEvents:
// notifications, addressing a player by name, and the session actions System
// Events owns. AppleScript is handed its strings as arguments rather than
// pasted into the source, which is what keeps a message holding a quote from
// becoming a syntax error.
//
// Some of these need the agent to hold a permission the user grants once:
// notifications ask on first use, and locking the screen through System Events
// needs Accessibility. Both report the refusal, so a run says why rather than
// quietly doing nothing.

// volumeStep is how far one volume up or down moves, in percent.
const volumeStep = 5

// defaultMacPlayer is the application the playback actions address when none is
// named. Music is the one every install has.
const defaultMacPlayer = "Music"

// osascriptArgs runs an AppleScript that reads its inputs from argv.
func osascriptArgs(ctx context.Context, script string, args ...string) (string, error) {
	full := append([]string{"-e", "on run argv", "-e", script, "-e", "end run"}, args...)
	return toolsh.RunTool(ctx, "osascript", full...)
}

// notifyDesktop shows a notification through Notification Center.
func notifyDesktop(ctx context.Context, n autoapi.Notification) error {
	_, err := osascriptArgs(ctx,
		"display notification (item 1 of argv) with title (item 2 of argv)",
		n.Message, n.Title)
	return err
}

// idleSeconds reads HIDIdleTime from the HID system, which is nanoseconds
// since the last keyboard or mouse event. It is the only idle measurement
// macOS exposes without a signed helper, read from the IO registry directly.
func idleSeconds(ctx context.Context, now time.Time) (float64, string, error) {
	nanos, err := hidIdleNanos()
	if err != nil {
		return 0, "", err
	}
	return idleparse.FromNanos(nanos), "iokit", nil
}

// mediaControl drives the player through AppleScript and the volume through
// CoreAudio's default output device, so it applies whatever is playing. The
// player half stays on osascript because addressing an application by name is
// AppleEvents, and the only native route is a private framework.
func mediaControl(ctx context.Context, action, player string, volume float64) error {
	switch action {
	case mediaVolumeSet:
		return setMacOutputVolume(volume)
	case mediaVolumeUp, mediaVolumeDown:
		current, err := macOutputVolume()
		if err != nil {
			return err
		}
		target := current + volumeStep
		if action == mediaVolumeDown {
			target = current - volumeStep
		}
		return setMacOutputVolume(target)
	case mediaMute:
		return setMacOutputMuted(true)
	case mediaUnmute:
		return setMacOutputMuted(false)
	case mediaMuteToggle:
		muted, err := macOutputMuted()
		if err != nil {
			return err
		}
		return setMacOutputMuted(!muted)
	}

	if player == "" {
		player = defaultMacPlayer
	}
	var verb string
	switch action {
	case mediaPlayPause:
		verb = "playpause"
	case mediaPlay:
		verb = "play"
	case mediaPause:
		verb = "pause"
	case mediaStop:
		verb = "stop"
	case mediaNext:
		verb = "next track"
	case mediaPrevious:
		verb = "previous track"
	default:
		return fmt.Errorf("unknown media action %q", action)
	}
	_, err := osascriptArgs(ctx,
		"tell application (item 1 of argv) to "+verb, player)
	return err
}

// clipboardRead returns the clipboard's text through NSPasteboard. macOS has
// one clipboard, so the primary selection reads the same board.
func clipboardRead(ctx context.Context, selection string) (string, error) {
	return pasteboardRead()
}

// clipboardWrite puts text on the clipboard.
func clipboardWrite(ctx context.Context, selection, text string) error {
	return pasteboardWrite(text)
}

// openURL opens a link in the default browser through NSWorkspace.
func openURL(ctx context.Context, url string) error {
	return workspaceOpenURL(url)
}

// openApp launches an application by name or by path and leaves it running,
// through NSWorkspace, with the open tool behind it.
func openApp(ctx context.Context, app string, args []string) error {
	if err := workspaceLaunchApp(app, args); err == nil {
		return nil
	}

	openArgs := []string{app}
	if !strings.ContainsRune(app, os.PathSeparator) {
		openArgs = []string{"-a", app}
	}
	if len(args) > 0 {
		openArgs = append(openArgs, "--args")
		openArgs = append(openArgs, args...)
	}
	_, err := toolsh.RunTool(ctx, "open", openArgs...)
	return err
}

// powerControl performs a session or power action. The session verbs are
// AppleEvents to the system process, sent in-process (QA1134's mechanism, the
// same request the osascript lines made through System Events); sleep rides
// the same event, which is what pmset asked for. The tools stay behind each
// for a host that refuses the direct send. Locking goes through the login
// framework, then CGSession, then the lock keystroke, which needs
// Accessibility.
func powerControl(ctx context.Context, action string) error {
	switch action {
	case powerLock:
		if err := lockScreenNative(); err == nil {
			return nil
		}
		const cgSession = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession"
		if _, err := os.Stat(cgSession); err == nil {
			if _, err := toolsh.RunTool(ctx, cgSession, "-suspend"); err == nil {
				return nil
			}
		}
		_, err := toolsh.RunTool(ctx, "osascript", "-e",
			`tell application "System Events" to keystroke "q" using {control down, command down}`)
		return err
	case powerSleep:
		if err := sendSystemAppleEvent(aeEventSleep); err == nil {
			return nil
		}
		_, err := toolsh.RunTool(ctx, "pmset", "sleepnow")
		return err
	case powerLogout:
		// The really-log-out event skips the confirmation dialog nobody is
		// there to answer.
		if err := sendSystemAppleEvent(aeEventReallyLogOut); err == nil {
			return nil
		}
		_, err := toolsh.RunTool(ctx, "osascript", "-e", `tell application "System Events" to log out`)
		return err
	case powerRestart:
		if err := sendSystemAppleEvent(aeEventRestart); err == nil {
			return nil
		}
		_, err := toolsh.RunTool(ctx, "osascript", "-e", `tell application "System Events" to restart`)
		return err
	case powerShutdown:
		if err := sendSystemAppleEvent(aeEventShutDown); err == nil {
			return nil
		}
		_, err := toolsh.RunTool(ctx, "osascript", "-e", `tell application "System Events" to shut down`)
		return err
	}
	return fmt.Errorf("unknown power action %q", action)
}

// speakText reads text out through NSSpeechSynthesizer, with say — the tool
// over the same engine — behind it for a process that cannot reach the class.
func speakText(ctx context.Context, text, voice string, rate int, wait bool) error {
	if err := speakNS(ctx, text, voice, rate, wait); err == nil || !errors.Is(err, errNoNSSpeech) {
		return err
	}

	tool, ok := toolsh.ToolPath("say")
	if !ok {
		return autoapi.MissingTool("speak", "say")
	}
	args := []string{}
	if voice != "" {
		args = append(args, "-v", voice)
	}
	if rate > 0 {
		args = append(args, "-r", strconv.Itoa(rate))
	}
	args = append(args, "--", text)
	if !wait {
		return toolsh.StartDetached(tool, args...)
	}
	_, err := toolsh.RunTool(ctx, tool, args...)
	return err
}
