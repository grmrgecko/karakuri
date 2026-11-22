//go:build !windows && !darwin

// Package linuxdesk is the automation backend for Linux, FreeBSD, and the other
// Unixes. It drives an X11 session through the X server directly (XTEST for
// input, EWMH for windows) and reaches the rest of the desktop through whatever
// tools the session has installed, falling back to the Wayland helpers where
// they are present. It is the whole answer on FreeBSD; on Linux the parent
// package layers the KDE and portal backends over it for a Wayland session.
package linuxdesk

import (
	"context"
	"image"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Local aliases for the vocabularies the primitives switch on, so the switches
// below read as they did before the move and cannot drift from the contract.
const (
	buttonLeft   = string(autoapi.ButtonLeft)
	buttonMiddle = string(autoapi.ButtonMiddle)
	buttonRight  = string(autoapi.ButtonRight)

	windowActivate = string(autoapi.WindowActivate)
	windowMinimize = string(autoapi.WindowMinimize)
	windowMaximize = string(autoapi.WindowMaximize)
	windowClose    = string(autoapi.WindowClose)

	primarySelection = string(autoapi.SelectionPrimary)

	mediaPlayPause  = autoapi.MediaPlayPause
	mediaPlay       = autoapi.MediaPlay
	mediaPause      = autoapi.MediaPause
	mediaStop       = autoapi.MediaStop
	mediaNext       = autoapi.MediaNext
	mediaPrevious   = autoapi.MediaPrevious
	mediaVolumeSet  = autoapi.MediaVolumeSet
	mediaVolumeUp   = autoapi.MediaVolumeUp
	mediaVolumeDown = autoapi.MediaVolumeDown
	mediaMute       = autoapi.MediaMute
	mediaUnmute     = autoapi.MediaUnmute
	mediaMuteToggle = autoapi.MediaMuteToggle

	powerLock     = string(autoapi.PowerLock)
	powerSleep    = string(autoapi.PowerSleep)
	powerLogout   = string(autoapi.PowerLogout)
	powerRestart  = string(autoapi.PowerRestart)
	powerShutdown = string(autoapi.PowerShutdown)
)

// Set is the backend's capability implementations. The parent package assigns
// each into a Session field, overriding individual ones with a KDE or portal
// backend on a Wayland Linux host.
type Set struct {
	Keyboard  autoapi.Keyboard
	Mouse     autoapi.Mouse
	Screen    autoapi.Screen
	Window    autoapi.Window
	Clipboard autoapi.Clipboard
	Notifier  autoapi.Notifier
	Media     autoapi.Media
	Power     autoapi.Power
	Idle      autoapi.Idle
	Opener    autoapi.Opener
	Speaker   autoapi.Speaker
}

// New returns the unix backend's capability set.
func New() Set {
	return Set{
		Keyboard:  keyboard{},
		Mouse:     mouse{},
		Screen:    screen{},
		Window:    window{},
		Clipboard: clip{},
		Notifier:  notifier{},
		Media:     media{},
		Power:     power{},
		Idle:      idle{},
		Opener:    opener{},
		Speaker:   speaker{},
	}
}

// Each capability is a zero-size adapter over the free functions in this
// package. Media and Power both name their method Control, so they cannot share
// one receiver — one type per capability keeps them apart.

type keyboard struct{}

func (keyboard) TypeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	return typeText(ctx, clock, text, delay)
}
func (keyboard) PressKeys(ctx context.Context, k autoapi.KeyStroke) error { return pressKeys(ctx, k) }

type mouse struct{}

func (mouse) Move(ctx context.Context, x, y int, relative bool) error {
	return moveMouse(ctx, x, y, relative)
}
func (mouse) Click(ctx context.Context, button autoapi.Button, count int) error {
	return clickMouse(ctx, string(button), count)
}
func (mouse) Hold(ctx context.Context, button autoapi.Button, down bool) error {
	return holdMouse(ctx, string(button), down)
}
func (mouse) Scroll(ctx context.Context, amount int, horizontal bool) error {
	return scrollMouse(ctx, amount, horizontal)
}
func (mouse) Position(ctx context.Context) (int, int, error) { return mousePoint(ctx) }

type screen struct{}

func (screen) Capture(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	return captureScreen(ctx, req)
}

type window struct{}

func (window) List(ctx context.Context) ([]autoapi.WindowInfo, error) { return listWindows(ctx) }
func (window) Act(ctx context.Context, w autoapi.WindowInfo, action autoapi.WindowAction) error {
	return windowAction(ctx, w, string(action))
}

type clip struct{}

func (clip) Read(ctx context.Context, sel autoapi.Selection) (string, error) {
	return clipboardRead(ctx, string(sel))
}
func (clip) Write(ctx context.Context, sel autoapi.Selection, text string) error {
	return clipboardWrite(ctx, string(sel), text)
}

type notifier struct{}

func (notifier) Notify(ctx context.Context, n autoapi.Notification) error {
	return notifyDesktop(ctx, n)
}

type media struct{}

func (media) Control(ctx context.Context, req autoapi.MediaRequest) error {
	return mediaControl(ctx, req.Action, req.Player, req.Volume)
}

type power struct{}

func (power) Control(ctx context.Context, action autoapi.PowerAction) error {
	return powerControl(ctx, string(action))
}

type idle struct{}

func (idle) Since(ctx context.Context, now time.Time) (float64, string, error) {
	return idleSeconds(ctx, now)
}

type opener struct{}

func (opener) OpenURL(ctx context.Context, url string) error { return openURL(ctx, url) }
func (opener) OpenApp(ctx context.Context, app string, args []string) error {
	return openApp(ctx, app, args)
}

type speaker struct{}

func (speaker) Speak(ctx context.Context, req autoapi.SpeakRequest) error {
	return speakText(ctx, req.Text, req.Voice, req.Rate, req.Wait)
}
