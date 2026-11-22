package autoapi

import (
	"strings"
	"time"
)

// Selection names which of the two shared text buffers a clipboard call reaches.
// Primary is the X11 and Wayland middle-click buffer; macOS and Windows have
// only the clipboard and use it for both.
type Selection string

// Clipboard selections.
const (
	SelectionClipboard Selection = "clipboard"
	SelectionPrimary   Selection = "primary"
)

// Button names a mouse button.
type Button string

// Mouse buttons.
const (
	ButtonLeft   Button = "left"
	ButtonMiddle Button = "middle"
	ButtonRight  Button = "right"
)

// WindowAction is one thing that can be done to a window.
type WindowAction string

// Window actions.
const (
	WindowActivate WindowAction = "activate"
	WindowMinimize WindowAction = "minimize"
	WindowMaximize WindowAction = "maximize"
	WindowClose    WindowAction = "close"
)

// PowerAction is one session or power action.
type PowerAction string

// Power actions.
const (
	PowerLock     PowerAction = "lock"
	PowerSleep    PowerAction = "sleep"
	PowerLogout   PowerAction = "logout"
	PowerRestart  PowerAction = "restart"
	PowerShutdown PowerAction = "shutdown"
)

// SessionAction is one thing that can be done to a login session.
type SessionAction string

// Session actions. Activate is what "log in" means where no password is
// involved: the session already exists and is brought to the foreground.
// Greeter is its counterpart, putting the login screen on the seat so a person
// can start one. Nothing here authenticates — no platform offers a way to log a
// user in unattended, and a node that pretended to would be lying about it.
const (
	SessionActivate SessionAction = "activate"
	SessionUnlock   SessionAction = "unlock"
	SessionLock     SessionAction = "lock"
	SessionLogout   SessionAction = "logout"
	SessionGreeter  SessionAction = "greeter"
	// SessionLogin is the credentialed switch-and-unlock: bring the target
	// user's session to the foreground and dismiss its lock with the supplied
	// password. It only ever travels in a LoginRequest, never through Act.
	SessionLogin SessionAction = "login"
)

// LoginRequest is a credentialed session action. The password is proven against
// User before anything happens; Target names the session to act on, and Action
// is SessionUnlock (dismiss the lock in place) or SessionLogin (switch to the
// session first, then dismiss its lock).
type LoginRequest struct {
	User   string
	Secret string
	Target SessionInfo
	Action SessionAction
}

// SessionClass groups a session by what it is for. Only User sessions are a
// person's desktop; Greeter is the login screen and Manager is the per-user
// service manager that owns no display.
const (
	SessionClassUser    = "user"
	SessionClassGreeter = "greeter"
	SessionClassManager = "manager"
)

// AccountName reduces DOMAIN\user or user@realm to the bare account name.
// Windows reports its accounts domain-qualified, and a caller matching a
// session by the user's name means the account, not the qualified form.
func AccountName(user string) string {
	if i := strings.LastIndex(user, `\`); i >= 0 {
		return user[i+1:]
	}
	if i := strings.IndexByte(user, '@'); i >= 0 {
		return user[:i]
	}
	return user
}

// SessionInfo describes one login session on the host. Fields a platform does
// not report are left zero rather than guessed; LockKnown is what separates
// "not locked" from "this host does not say".
type SessionInfo struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	UID       int       `json:"uid"`
	Seat      string    `json:"seat,omitempty"`
	TTY       string    `json:"tty,omitempty"`
	Display   string    `json:"display,omitempty"`
	Type      string    `json:"type,omitempty"`  // "wayland", "x11", "tty", "console", "rdp"
	Class     string    `json:"class,omitempty"` // one of the SessionClass names
	State     string    `json:"state,omitempty"` // "active", "online", "closing", "disconnected"
	Active    bool      `json:"active"`
	Remote    bool      `json:"remote"`
	Locked    bool      `json:"locked"`
	LockKnown bool      `json:"lock_known"`
	Since     time.Time `json:"since,omitempty"`
}

// Graphical reports whether the session is a person's desktop, which is what a
// caller looking for "the session someone is sitting at" means.
func (s SessionInfo) Graphical() bool {
	return s.Class == SessionClassUser && (s.Type == "wayland" || s.Type == "x11" || s.Type == "console")
}

// Media actions, the vocabulary of MediaRequest.Action.
const (
	MediaPlayPause  = "playpause"
	MediaPlay       = "play"
	MediaPause      = "pause"
	MediaStop       = "stop"
	MediaNext       = "next"
	MediaPrevious   = "previous"
	MediaVolumeSet  = "volume_set"
	MediaVolumeUp   = "volume_up"
	MediaVolumeDown = "volume_down"
	MediaMute       = "mute"
	MediaUnmute     = "unmute"
	MediaMuteToggle = "mute_toggle"
)

// Dialog kinds. The first four state something and are answered by dismissing
// them; the last three collect a value.
const (
	DialogInfo     = "info"
	DialogWarning  = "warning"
	DialogError    = "error"
	DialogQuestion = "question"
	DialogEntry    = "entry"
	DialogPassword = "password"
	DialogList     = "list"
)

// CaptureRequest describes what a screenshot should photograph. A region is read
// in the chosen screen's own coordinates, so a caller aimed at the second
// monitor does not have to know where the desktop puts it.
type CaptureRequest struct {
	// DesktopCoordinates uses desktop coordinates for a region, across screens.
	DesktopCoordinates bool
	Display            int
	Region             bool
	X                  int
	Y                  int
	Width              int
	Height             int
}

// WindowInfo is one window on the host's screen, in the shape both the listing
// and the actions report it.
type WindowInfo struct {
	Image     string
	Enabled   *bool
	CaptureID string
	ID        string
	Title     string
	Class     string
	PID       int
	Active    bool
	X         int
	Y         int
	Width     int
	Height    int
}

// Notification is one desktop message.
type Notification struct {
	Title   string
	Message string
	Urgency string
	Icon    string
}

// DialogRequest describes a dialog to open. Timeout of zero lets the caller's
// context govern how long the dialog may stay open.
type DialogRequest struct {
	Kind         string
	Title        string
	Text         string
	DefaultValue string
	Choices      []string
	OKLabel      string
	CancelLabel  string
	Timeout      time.Duration
}

// DialogResult is what a person did with a dialog. Cancelling is an answer of
// its own (Answered false), and a dialog nobody reached in time is TimedOut
// rather than an error: the caller asked and learned there was no one there.
type DialogResult struct {
	Value    string
	Answered bool
	TimedOut bool
}

// SpeakRequest describes something to say aloud.
type SpeakRequest struct {
	Text  string
	Voice string
	Rate  int
	Wait  bool
}

// MediaRequest is one media-player command.
type MediaRequest struct {
	Action string
	Player string
	Volume float64
}
