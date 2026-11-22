package autoapi

import (
	"context"
	"image"
	"time"
)

// The capability interfaces are the contract a backend implements and a caller
// programs against. A Session (in the parent package) holds one value per
// capability, chosen for the host at construction; an unavailable capability is
// a stub whose methods return a clear error rather than a nil field.

// Keyboard types text and presses key combinations in the session.
type Keyboard interface {
	TypeText(ctx context.Context, clock Clock, text string, delay time.Duration) error
	PressKeys(ctx context.Context, k KeyStroke) error
}

// Mouse moves, clicks, and scrolls the pointer. Position reports where the
// pointer is, which no Wayland session exposes to a client (ErrUnsupported).
type Mouse interface {
	Move(ctx context.Context, x, y int, relative bool) error
	Click(ctx context.Context, button Button, count int) error
	Hold(ctx context.Context, button Button, down bool) error
	Scroll(ctx context.Context, amount int, horizontal bool) error
	Position(ctx context.Context) (x, y int, err error)
}

// Screen photographs the display.
type Screen interface {
	Capture(ctx context.Context, req CaptureRequest) (image.Image, error)
}

// Window lists the windows on the screen and acts on one.
type Window interface {
	List(ctx context.Context) ([]WindowInfo, error)
	Act(ctx context.Context, w WindowInfo, action WindowAction) error
}

// Clipboard reads and writes the session's shared text buffers.
type Clipboard interface {
	Read(ctx context.Context, sel Selection) (string, error)
	Write(ctx context.Context, sel Selection, text string) error
}

// Notifier posts a desktop notification.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Dialog asks the user something and reports what they answered.
type Dialog interface {
	Ask(ctx context.Context, req DialogRequest) (DialogResult, error)
}

// Speaker says text aloud through the session's speech synthesizer.
type Speaker interface {
	Speak(ctx context.Context, req SpeakRequest) error
}

// Media controls the session's media players.
type Media interface {
	Control(ctx context.Context, req MediaRequest) error
}

// Power performs a session or power action.
type Power interface {
	Control(ctx context.Context, action PowerAction) error
}

// Idle reports how long the session has been idle. It takes the current time so
// a caller with an injected clock stays deterministic; source names how the
// reading was obtained.
type Idle interface {
	Since(ctx context.Context, now time.Time) (seconds float64, source string, err error)
}

// Opener launches URLs and applications in the session.
type Opener interface {
	OpenURL(ctx context.Context, url string) error
	OpenApp(ctx context.Context, app string, args []string) error
}

// Sessions lists the host's login sessions and acts on one. It is the only
// capability that reaches outside the caller's own session, so most of it needs
// privilege: acting on another user's session is an administrator's action on
// every platform, and a caller running as that user can only reach their own.
// It is also the one capability that must not be delegated to a session helper,
// since the helper is the unprivileged user the delegation exists to become.
type Sessions interface {
	List(ctx context.Context) ([]SessionInfo, error)
	Act(ctx context.Context, s SessionInfo, action SessionAction) error
	// Login performs a credentialed action: prove the account's password, then
	// unlock or switch-and-unlock the target session. Unlike Act's password-free
	// verbs, this authenticates first, so it is the only path that dismisses a
	// lock or crosses into a session on the strength of a supplied password —
	// which is why it stays in the privileged process rather than a delegated
	// helper. A platform that can only reach its lock screen through a signed
	// system component (macOS, Windows) reports that the component is needed.
	Login(ctx context.Context, req LoginRequest) error
}
