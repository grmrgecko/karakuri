package automation

import (
	"errors"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// The names one capability call is addressed by. Client and server read the
// same constants, so a method renamed on one side fails to build on the other
// rather than becoming an unknown-method error at run time.
const (
	capKeyboard  = "keyboard"
	capMouse     = "mouse"
	capScreen    = "screen"
	capWindow    = "window"
	capClipboard = "clipboard"
	capNotifier  = "notifier"
	capDialog    = "dialog"
	capSpeaker   = "speaker"
	capMedia     = "media"
	capPower     = "power"
	capIdle      = "idle"
	capOpener    = "opener"
	// capSessions carries the session verbs that need to run inside the
	// session itself. On macOS the lock, the switch, and the login-window
	// verb are refused to every process outside the graphical session, so a
	// privileged parent asks the helper to perform them.
	capSessions = "sessions"
)

const (
	mTypeText  = "type_text"
	mPressKeys = "press_keys"
	mMove      = "move"
	mClick     = "click"
	mHold      = "hold"
	mScroll    = "scroll"
	mPosition  = "position"
	mCapture   = "capture"
	mList      = "list"
	mAct       = "act"
	mRead      = "read"
	mWrite     = "write"
	mNotify    = "notify"
	mAsk       = "ask"
	mSpeak     = "speak"
	mControl   = "control"
	mSince     = "since"
	mOpenURL   = "open_url"
	mOpenApp   = "open_app"
)

// Call argument and result shapes. They are structs rather than positional
// arrays so a field added later reaches an older peer as a zero value instead
// of shifting everything after it.
type (
	typeTextArgs struct {
		Text  string        `json:"text"`
		Delay time.Duration `json:"delay"`
	}
	moveArgs struct {
		X        int  `json:"x"`
		Y        int  `json:"y"`
		Relative bool `json:"relative"`
	}
	clickArgs struct {
		Button autoapi.Button `json:"button"`
		Count  int            `json:"count"`
	}
	holdArgs struct {
		Button autoapi.Button `json:"button"`
		Down   bool           `json:"down"`
	}
	scrollArgs struct {
		Amount     int  `json:"amount"`
		Horizontal bool `json:"horizontal"`
	}
	positionResult struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	captureResult struct {
		PNG     []byte                  `json:"png"`
		Mapping *autoapi.CaptureMapping `json:"mapping,omitempty"`
	}
	windowActArgs struct {
		Window autoapi.WindowInfo   `json:"window"`
		Action autoapi.WindowAction `json:"action"`
	}
	clipboardReadArgs struct {
		Selection autoapi.Selection `json:"selection"`
	}
	clipboardWriteArgs struct {
		Selection autoapi.Selection `json:"selection"`
		Text      string            `json:"text"`
	}
	textResult struct {
		Text string `json:"text"`
	}
	sinceArgs struct {
		Now time.Time `json:"now"`
	}
	sinceResult struct {
		Seconds float64 `json:"seconds"`
		Source  string  `json:"source"`
	}
	openURLArgs struct {
		URL string `json:"url"`
	}
	openAppArgs struct {
		App  string   `json:"app"`
		Args []string `json:"args"`
	}
	sessionActArgs struct {
		Target autoapi.SessionInfo   `json:"target"`
		Action autoapi.SessionAction `json:"action"`
	}
)

// toWire flattens an error into something that survives the crossing with its
// kind. Only the two kinds a caller branches on are carried; everything else is
// text, because a caller that only prints it loses nothing.
func toWire(err error) *hrpc.WireError {
	if err == nil {
		return nil
	}
	switch {
	case autoapi.IsMissingTool(err):
		return &hrpc.WireError{Kind: hrpc.KindMissingTool, Message: err.Error()}
	case errors.Is(err, autoapi.ErrUnsupported):
		return &hrpc.WireError{Kind: hrpc.KindUnsupported, Message: err.Error()}
	}
	return &hrpc.WireError{Kind: hrpc.KindPlain, Message: err.Error()}
}

// fromWire rebuilds the error on the calling side. The reconstructed value has
// to satisfy the same checks the original did — IsMissingTool and
// errors.Is(err, ErrUnsupported) — since that is the whole reason the kind
// travels; the helper's message is kept as the text, because it names the
// tools and the session the caller's side never saw.
func fromWire(e *hrpc.WireError) error {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case hrpc.KindMissingTool:
		return missingToolWith(e.Message)
	case hrpc.KindUnsupported:
		return &unsupportedError{msg: e.Message}
	}
	return errors.New(e.Message)
}

// unsupportedError carries the helper's message while still matching
// errors.Is(err, ErrUnsupported).
type unsupportedError struct{ msg string }

func (e *unsupportedError) Error() string { return e.msg }
func (e *unsupportedError) Is(target error) bool {
	return target == autoapi.ErrUnsupported
}

// missingToolWith rebuilds a missing-tool failure carrying the helper's own
// wording, so IsMissingTool still answers true on this side.
func missingToolWith(msg string) error {
	return &wireMissingTool{msg: msg, inner: autoapi.MissingTool("reach the session", "the session helper")}
}

// wireMissingTool is a missing-tool failure whose text is the helper's and
// whose identity is the module's, so errors.As finds it.
type wireMissingTool struct {
	msg   string
	inner error
}

func (e *wireMissingTool) Error() string { return e.msg }
func (e *wireMissingTool) Unwrap() error { return e.inner }
