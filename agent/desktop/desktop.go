package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// The desktop nodes drive the session the agent process belongs to. They work
// because the agent runs as the user whose screen, clipboard, and audio they
// touch: an agent started by a system service manager has no session to reach,
// and the tools these nodes call say so. Running one agent per user on a
// machine is how each of those sessions gets its own set of nodes.

// Media actions. The volume ones are the sound device rather than the player,
// which is what a wall panel or a doorbell flow actually wants to change.
const (
	mediaPlayPause  = "playpause"
	mediaPlay       = "play"
	mediaPause      = "pause"
	mediaNext       = "next"
	mediaPrevious   = "previous"
	mediaStop       = "stop"
	mediaVolumeSet  = "volume_set"
	mediaVolumeUp   = "volume_up"
	mediaVolumeDown = "volume_down"
	mediaMute       = "mute"
	mediaUnmute     = "unmute"
	mediaMuteToggle = "mute_toggle"
)

// Power actions, from the least destructive to the most.
const (
	powerLock     = "lock"
	powerSleep    = "sleep"
	powerLogout   = "logout"
	powerRestart  = "restart"
	powerShutdown = "shutdown"
)

// Clipboard selections. Primary is the X11 and Wayland middle-click buffer;
// everywhere else it reads as the ordinary clipboard.
const (
	clipboardSelection = "clipboard"
	primarySelection   = "primary"
)

// Node type names this group contributes. They are the names an operator
// writes in the disable list and the names the host namespaces as
// agent.<id>.<name>, so they are constants rather than literals in the metadata.
const (
	nodeNotify         = "notify"
	nodeDialog         = "dialog"
	nodeSpeak          = "speak"
	nodeIdleTime       = "idle-time"
	nodeMedia          = "media"
	nodeMediaStatus    = "media-status"
	nodeClipboardRead  = "clipboard-read"
	nodeClipboardWrite = "clipboard-write"
	nodeOpenURL        = "open-url"
	nodeOpenApp        = "open-app"
	nodePower          = "power"
)

// notifyNode puts a message on the agent user's screen.
type notifyNode struct {
	sess    *automation.Session
	initErr error
}

func (notifyNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeNotify,
		Folder:      folder,
		Label:       "Notify",
		Description: "Shows a desktop notification in the agent user's session",
		Icon:        "las la-bell",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "title",
				Label:       "Title",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "Karakuri",
				Help:        "{{ }} segments are rendered against the message.",
			},
			{
				ID:          "message",
				Label:       "Message",
				Type:        flow.FieldText,
				Placeholder: "{{ payload }}",
				Help:        "Body text. Empty sends the payload, rendered as text.",
			},
			{
				ID:      "urgency",
				Label:   "Urgency",
				Type:    flow.FieldSelect,
				Default: "normal",
				Options: []nodeapi.Option{
					{Value: "low", Label: "Low"},
					{Value: "normal", Label: "Normal"},
					{Value: "critical", Label: "Critical"},
				},
				Help: "Linux only. macOS and Windows show every notification the same way.",
			},
			{
				ID:          "icon",
				Label:       "Icon",
				Type:        flow.FieldString,
				Placeholder: "dialog-information",
				Help:        "Linux only: an icon name from the desktop's theme, or a path to an image.",
			},
		},
	}
}

// notifyConfig is the decoded notify configuration.
type notifyConfig struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Urgency string `json:"urgency"`
	Icon    string `json:"icon"`
}

// Execute renders the notification and shows it.
func (n *notifyNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg notifyConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	title, err := agentnode.Interpolate(ec, cfg.Title, msg)
	if err != nil {
		return nil, err
	}
	body, err := agentnode.Interpolate(ec, cfg.Message, msg)
	if err != nil {
		return nil, err
	}
	if body == "" {
		body = agentnode.ValueText(msg.Payload)
	}
	if title == "" && body == "" {
		return nil, errors.New("notify has nothing to show")
	}
	if err := n.sess.Notifier.Notify(ctx, automation.Notification{
		Title:   title,
		Message: body,
		Urgency: cfg.Urgency,
		Icon:    cfg.Icon,
	}); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"notified": true})), nil
}

// idleTimeNode reports how long the agent user's session has been untouched.
// It is a pure source, so a switch or a filter can bind it over a value edge
// and a run costs one measurement however many nodes read it. Presence at a
// desk is the thing it is really reporting.
type idleTimeNode struct {
	sess    *automation.Session
	initErr error
}

func (idleTimeNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeIdleTime,
		Folder:            folder,
		Label:             "Idle time",
		Description:       "Seconds since the last keyboard or mouse input in the agent user's session",
		Icon:              "las la-user-clock",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Seconds", Type: flow.PortNumber},
		},
	}
}

// Execute measures the session's idle time.
func (n *idleTimeNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	seconds, source, err := n.sess.Idle.Since(ctx, ec.Clock().Now())
	if err != nil {
		return nil, err
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: seconds,
		Meta:    map[string]any{"source": source},
	}), nil
}

// mediaNode drives playback and the sound device in the agent user's session.
type mediaNode struct {
	sess    *automation.Session
	initErr error
}

func (mediaNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeMedia,
		Folder:      folder,
		Label:       "Media control",
		Description: "Controls playback and volume in the agent user's session",
		Icon:        "las la-play-circle",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "action",
				Label:   "Action",
				Type:    flow.FieldSelect,
				Default: mediaPlayPause,
				Options: []nodeapi.Option{
					{Value: mediaPlayPause, Label: "Play or pause"},
					{Value: mediaPlay, Label: "Play"},
					{Value: mediaPause, Label: "Pause"},
					{Value: mediaStop, Label: "Stop"},
					{Value: mediaNext, Label: "Next track"},
					{Value: mediaPrevious, Label: "Previous track"},
					{Value: mediaVolumeSet, Label: "Set volume"},
					{Value: mediaVolumeUp, Label: "Volume up"},
					{Value: mediaVolumeDown, Label: "Volume down"},
					{Value: mediaMute, Label: "Mute"},
					{Value: mediaUnmute, Label: "Unmute"},
					{Value: mediaMuteToggle, Label: "Toggle mute"},
				},
			},
			{
				ID:      "volume",
				Label:   "Volume",
				Type:    flow.FieldNumber,
				Default: mediaDefaultVolume,
				ShowIf:  &flow.ShowIf{Field: "action", Equals: mediaVolumeSet},
				Help:    "0 to 100. Wire it to a dashboard slider or a control to make the level follow something.",
			},
			{
				ID:          "player",
				Label:       "Player",
				Type:        flow.FieldString,
				Placeholder: "spotify",
				Help: "Which player the playback actions address: an MPRIS name on Linux, an " +
					"application name on macOS. Empty uses whatever is playing. Windows sends the " +
					"media keys, which the system routes on its own.",
			},
		},
	}
}

// mediaDefaultVolume is the level Set volume applies when the configuration
// names none.
const mediaDefaultVolume = 50

// mediaConfig is the decoded media configuration. Volume is a pointer so an
// absent field keeps the default the editor shows, which zero is not.
type mediaConfig struct {
	Action string   `json:"action"`
	Volume *float64 `json:"volume"`
	Player string   `json:"player"`
}

// Execute performs one media action.
func (n *mediaNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg mediaConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	action := cfg.Action
	if action == "" {
		action = mediaPlayPause
	}
	volume := float64(mediaDefaultVolume)
	if cfg.Volume != nil {
		volume = *cfg.Volume
	}
	if action == mediaVolumeSet && (volume < 0 || volume > 100) {
		return nil, fmt.Errorf("volume %g is outside 0 to 100", volume)
	}
	player, err := agentnode.Interpolate(ec, cfg.Player, msg)
	if err != nil {
		return nil, err
	}
	if err := n.sess.Media.Control(ctx, automation.MediaRequest{Action: action, Player: player, Volume: volume}); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"media_action": action})), nil
}

// clipboardReadNode reads the agent user's clipboard. It is a pure source, so
// a flow can bind the clipboard to a config field the same way it binds a file.
type clipboardReadNode struct {
	opts    agentnode.Options
	sess    *automation.Session
	initErr error
}

func (clipboardReadNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeClipboardRead,
		Folder:            folder,
		Label:             "Read clipboard",
		Description:       "Reads the clipboard in the agent user's session",
		Icon:              "las la-clipboard",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString},
		},
		ConfigSpec: []nodeapi.FieldSpec{selectionField()},
	}
}

// clipboardConfig is the decoded configuration of both clipboard nodes.
type clipboardConfig struct {
	Selection string `json:"selection"`
	Property  string `json:"property"`
}

// Execute reads the clipboard into the payload.
func (n *clipboardReadNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg clipboardConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	text, err := n.sess.Clipboard.Read(ctx, automation.Selection(cfg.Selection))
	if err != nil {
		return nil, err
	}
	originalSize := len(text)
	text = agentnode.CapText(text, agentnode.ReadLimit(n.opts.MaxOutput))
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: text,
		Meta: map[string]any{
			"selection": selectionName(cfg.Selection), "size": len(text),
			"original_size": originalSize, "truncated": len(text) < originalSize,
		},
	}), nil
}

// clipboardWriteNode puts a value on the agent user's clipboard.
type clipboardWriteNode struct {
	sess    *automation.Session
	initErr error
}

func (clipboardWriteNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeClipboardWrite,
		Folder:      folder,
		Label:       "Write clipboard",
		Description: "Puts a value on the clipboard in the agent user's session",
		Icon:        "las la-paste",
		Retryable:   true,
		Inputs: []nodeapi.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortAny,
			TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}},
		}},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "property",
				Label:       "Content",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload",
				Help:        "Dot path to the value copied. Objects and arrays are copied as JSON.",
			},
			selectionField(),
		},
	}
}

// Execute copies the configured value to the clipboard.
func (n *clipboardWriteNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg clipboardConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := agentnode.ResolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}
	text := agentnode.ValueText(value)
	if err := n.sess.Clipboard.Write(ctx, automation.Selection(cfg.Selection), text); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{
		"selection": selectionName(cfg.Selection),
		"size":      len(text),
	})), nil
}

// selectionField is the clipboard selector both clipboard nodes carry.
func selectionField() nodeapi.FieldSpec {
	return nodeapi.FieldSpec{
		ID:      "selection",
		Label:   "Selection",
		Type:    flow.FieldSelect,
		Default: clipboardSelection,
		Options: []nodeapi.Option{
			{Value: clipboardSelection, Label: "Clipboard"},
			{Value: primarySelection, Label: "Primary (middle-click)"},
		},
		Help: "Primary is an X11 and Wayland selection. macOS and Windows have only the clipboard, and use it for both.",
	}
}

// selectionName normalizes the selection for metadata.
func selectionName(selection string) string {
	if selection == primarySelection {
		return primarySelection
	}
	return clipboardSelection
}

// openURLNode opens a link in the agent user's browser.
type openURLNode struct {
	sess    *automation.Session
	initErr error
}

func (openURLNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeOpenURL,
		Folder:      folder,
		Label:       "Open URL",
		Description: "Opens a link in the agent user's default browser",
		Icon:        "las la-external-link-alt",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "url",
				Label:       "URL",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "https://karakuri.local/dashboards/hallway",
				Help:        "{{ }} segments are rendered against the message. Only http and https are opened.",
			},
		},
	}
}

// openURLConfig is the decoded open-url configuration.
type openURLConfig struct {
	URL string `json:"url"`
}

// Execute opens the link.
func (n *openURLNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg openURLConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	target, err := agentnode.Interpolate(ec, cfg.URL, msg)
	if err != nil {
		return nil, err
	}
	// A handler resolves any scheme the desktop knows, including ones that run
	// a local program, so the node opens web links and nothing else.
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return nil, fmt.Errorf("%q is not an http or https URL", target)
	}
	if err := n.sess.Opener.OpenURL(ctx, target); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"opened": target})), nil
}

// openAppNode launches a program in the agent user's session and leaves it
// running. It differs from the command node in what happens next: a command is
// waited for and its output collected, while an application outlives the run.
type openAppNode struct {
	sess    *automation.Session
	initErr error
}

func (openAppNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeOpenApp,
		Folder:      folder,
		Label:       "Open application",
		Description: "Launches an application in the agent user's session and leaves it running",
		Icon:        "las la-rocket",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "app",
				Label:       "Application",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "firefox",
				Help: "A program in PATH, an absolute path, a .desktop id on Linux, or an " +
					"application name on macOS.",
			},
			{
				ID:      "args",
				Label:   "Arguments",
				Type:    flow.FieldJSON,
				Control: "list",
				Help:    "One entry per argument, rendered against the message.",
			},
		},
	}
}

// openAppConfig is the decoded open-app configuration.
type openAppConfig struct {
	App  string   `json:"app"`
	Args []string `json:"args"`
}

// Execute launches the application.
func (n *openAppNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg openAppConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	app, err := agentnode.Interpolate(ec, cfg.App, msg)
	if err != nil {
		return nil, err
	}
	if app == "" {
		return nil, errors.New("open application has no application")
	}
	args := make([]string, 0, len(cfg.Args))
	for i, raw := range cfg.Args {
		// An argument is passed as written, as Run Command passes its own.
		rendered, err := ec.Interpolate(raw, msg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: invalid template %q: %w", i+1, raw, err)
		}
		args = append(args, rendered)
	}
	if err := n.sess.Opener.OpenApp(ctx, app, args); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"opened": app})), nil
}

// powerNode changes the session or power state of the agent host. Every action
// takes effect at once and some of them end the agent's own process, so the
// last thing a flow sees is the message this node emits before the machine
// acts on it.
type powerNode struct {
	sess    *automation.Session
	initErr error
}

func (powerNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodePower,
		Folder:      folder,
		Label:       "Power",
		Description: "Locks, sleeps, logs out, restarts, or shuts down the agent host",
		Icon:        "las la-power-off",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "action",
				Label:   "Action",
				Type:    flow.FieldSelect,
				Default: powerLock,
				Options: []nodeapi.Option{
					{Value: powerLock, Label: "Lock the session"},
					{Value: powerSleep, Label: "Sleep"},
					{Value: powerLogout, Label: "Log out"},
					{Value: powerRestart, Label: "Restart"},
					{Value: powerShutdown, Label: "Shut down"},
				},
				Help: "Sleep, restart, and shut down need the agent's user to be allowed to ask " +
					"for them, which is the desktop default and not the default for a system service.",
			},
		},
	}
}

// powerConfig is the decoded power configuration.
type powerConfig struct {
	Action string `json:"action"`
}

// Execute performs the power action. The message is emitted first, because the
// machine may not be there to emit it afterwards.
func (n *powerNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg powerConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	action := cfg.Action
	if action == "" {
		action = powerLock
	}
	ec.Logger().Info("power action requested", zap.String("action", action))
	if err := n.sess.Power.Control(ctx, automation.PowerAction(action)); err != nil {
		return nil, err
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"power_action": action})), nil
}
