// Package automation drives a host's keyboard, mouse, screen, windows,
// clipboard, and desktop session, cross-platform and without cgo. It is the
// automation surface karakuri's agent nodes are built on, extracted so it can
// be used on its own.
//
// New probes the host and returns a Session whose every capability is filled in
// for whatever the session actually is: an X11 or Wayland Linux desktop, macOS,
// Windows, or FreeBSD. A capability the host cannot provide is a stub that
// returns a clear error rather than a nil field, so a caller never has to guard
// one.
package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Re-exported contract, so a caller imports this one package. The definitions
// live in autoapi to break the import cycle between this package and the
// backends, which both need the types.
type (
	Keyboard  = autoapi.Keyboard
	Mouse     = autoapi.Mouse
	Screen    = autoapi.Screen
	Window    = autoapi.Window
	Clipboard = autoapi.Clipboard
	Notifier  = autoapi.Notifier
	Dialog    = autoapi.Dialog
	Speaker   = autoapi.Speaker
	Media     = autoapi.Media
	Power     = autoapi.Power
	Idle      = autoapi.Idle
	Opener    = autoapi.Opener
	Sessions  = autoapi.Sessions

	KeyStroke      = autoapi.KeyStroke
	WindowInfo     = autoapi.WindowInfo
	CaptureRequest = autoapi.CaptureRequest
	CaptureMapping = autoapi.CaptureMapping
	Notification   = autoapi.Notification
	DialogRequest  = autoapi.DialogRequest
	DialogResult   = autoapi.DialogResult
	SpeakRequest   = autoapi.SpeakRequest
	MediaRequest   = autoapi.MediaRequest
	SessionInfo    = autoapi.SessionInfo
	LoginRequest   = autoapi.LoginRequest

	Button        = autoapi.Button
	Selection     = autoapi.Selection
	WindowAction  = autoapi.WindowAction
	PowerAction   = autoapi.PowerAction
	SessionAction = autoapi.SessionAction

	Clock      = autoapi.Clock
	RealClock  = autoapi.RealClock
	TokenStore = autoapi.TokenStore
)

// Session action and class vocabularies, re-exported so a caller naming one
// does not have to import autoapi for a string constant.
const (
	SessionActivate = autoapi.SessionActivate
	SessionUnlock   = autoapi.SessionUnlock
	SessionLock     = autoapi.SessionLock
	SessionLogout   = autoapi.SessionLogout
	SessionGreeter  = autoapi.SessionGreeter
	SessionLogin    = autoapi.SessionLogin

	SessionClassUser    = autoapi.SessionClassUser
	SessionClassGreeter = autoapi.SessionClassGreeter
	SessionClassManager = autoapi.SessionClassManager
)

var (
	ParseKeyStroke = autoapi.ParseKeyStroke
	FileTokenStore = autoapi.FileTokenStore
	MissingTool    = autoapi.MissingTool
	IsMissingTool  = autoapi.IsMissingTool
	ErrUnsupported = autoapi.ErrUnsupported
)

// Options configures a Session. The zero value is valid: it persists no portal
// token and uses the default identity label. The keystroke clock is not here —
// it is passed per call through Keyboard.TypeText, so a caller with an injected
// clock stays deterministic without configuring the session.
type Options struct {
	// TokenStore persists the RemoteDesktop portal's restore_token so a second
	// session restores without prompting; nil means no persistence.
	TokenStore autoapi.TokenStore
	// AppID labels the automation to the portal and any tool that shows one;
	// empty means "karakuri".
	AppID string
	// Logf is an optional sink for backend diagnostics; nil discards them.
	Logf func(format string, args ...any)
	// HelperCommand is the argv this binary is re-executed with to serve as a
	// session helper, and naming it is what opts a caller into delegation. The
	// command it names has to call ServeHelper over its standard input and
	// output. On macOS it also enables delegation from SSH into the same user's
	// GUI session. An empty value disables delegation.
	HelperCommand []string
	// SecureInputCommand is the argv this binary is re-executed with to type
	// into a locked Windows console. The command it names has to call
	// ServeSecureInputCommand over its standard input and output. The helper
	// is started as SYSTEM inside the target session, so credentialed unlock
	// on Windows is a SYSTEM agent's action; an empty value means the session
	// backend reports what is missing instead of pretending to type.
	SecureInputCommand []string
}

// appID returns the configured identity label or the default.
func (o Options) appID() string {
	if o.AppID != "" {
		return o.AppID
	}
	return "karakuri"
}

// Environment records what New detected, for Describe and for a caller that
// wants to log which backend answered a capability.
type Environment struct {
	OS         string            // "linux", "darwin", "windows", "freebsd"
	Display    string            // "x11", "wayland", or ""
	Compositor string            // "kwin", "wlroots", "gnome", "cosmic", or ""
	Backends   map[string]string // capability name -> chosen backend
	// Delegate names the session a privileged process handed its desktop
	// capabilities to, and is empty when it is acting on its own session.
	Delegate string
}

// Session is the composed automation surface. Every field is non-nil after New;
// an unavailable capability is a stub whose methods return a clear error.
type Session struct {
	Desktop    Desktop
	Inspection autoapi.Inspection
	Keyboard   autoapi.Keyboard
	Mouse      autoapi.Mouse
	Screen     autoapi.Screen
	Window     autoapi.Window
	Clipboard  autoapi.Clipboard
	Notifier   autoapi.Notifier
	Dialog     autoapi.Dialog
	Speaker    autoapi.Speaker
	Media      autoapi.Media
	Power      autoapi.Power
	Idle       autoapi.Idle
	Opener     autoapi.Opener
	Sessions   autoapi.Sessions

	input   inputGate
	env     Environment
	closers []func() error
}

// New probes the host and returns a Session wired for it. A privileged process
// that named a HelperCommand and found a desktop session belonging to somebody
// else gets one whose capabilities run in a helper owned by that user, since a
// desktop answers its own user and nobody else. On macOS, an SSH caller that
// names a HelperCommand also delegates into its own graphical session.
func New(ctx context.Context, opts Options) (*Session, error) {
	if target, ok := findDelegateTarget(ctx, opts); ok {
		return newDelegated(ctx, opts, target)
	}
	s, err := buildSession(ctx, opts)
	if err != nil {
		return nil, err
	}
	wireInput(s)
	s.Inspection = newInspection()
	s.Desktop = localDesktop{s}
	s.Window = enrichWindowBackend(s.Window)
	s.env.Backends["inspection"] = inspectionBackend
	wireUnlockTyping(s)
	return s, nil
}

// wireUnlockTyping hands the composed session's keyboard capability to the
// session backend when it can use one. The macOS unlock types the password
// into the lock screen, and which process those events may come from depends
// on the composition: a privileged parent cannot deliver events to a console
// session it is not part of, so a delegated composition points the backend at
// the helper's keyboard. Backends without a use for it never match the hook.
func wireUnlockTyping(s *Session) {
	if kc, ok := s.Sessions.(interface{ SetKeyboard(autoapi.Keyboard) }); ok {
		kc.SetKeyboard(s.Keyboard)
	}
}

// Describe reports what New detected and which backend it chose per capability.
func (s *Session) Describe() Environment { return s.env }

// Close releases anything a backend holds open, such as an X connection or a
// portal session. It is safe to call on a Session whose backends hold nothing.
func (s *Session) Close() error {
	var first error
	for _, c := range s.closers {
		if err := c(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
