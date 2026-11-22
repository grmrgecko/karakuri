package system

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// The two session nodes are to login sessions what the process nodes are to
// the process table: one reads and one acts. They are the only nodes that
// reach outside the account the agent runs as, so most of what they do needs a
// privileged agent — a user can lock and unlock their own screen, and nothing
// else. What none of them can do is sign a user in from scratch: no platform
// offers that unattended, so "log in" means switch-and-unlock a session that
// already exists, with the account's password proven from a stored credential
// before anything moves.

// Node type names.
const (
	nodeSessionList = "session-list"
	nodeSession     = "session"
)

// Session targets, how the action node decides which session it means.
const (
	targetActive  = "active"
	targetID      = "id"
	targetUser    = "user"
	targetGreeter = "greeter"
)

// sessionListNode reports the host's login sessions.
type sessionListNode struct {
	sess    *automation.Session
	initErr error
}

func (sessionListNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeSessionList,
		Folder:            folder,
		Label:             "Session list",
		Description:       "Lists the login sessions on this agent host and who is signed into them",
		Icon:              "las la-users",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "include_system",
				Label:   "Include the login screen and service sessions",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Off lists only sessions a person is signed into. On adds the login screen " +
					"and the per-user service manager, which is what a flow switching to the " +
					"login screen needs to see.",
			},
		},
	}
}

// sessionListConfig is the decoded listing configuration.
type sessionListConfig struct {
	IncludeSystem bool `json:"include_system"`
}

// Execute reads the session table once and reports what is on it.
func (n *sessionListNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg sessionListConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}

	sessions, err := n.sess.Sessions.List(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0, len(sessions))
	var active map[string]any
	for _, s := range sessions {
		if !cfg.IncludeSystem && s.Class != automation.SessionClassUser {
			continue
		}
		row := sessionPayload(s)
		rows = append(rows, row)
		// The active graphical session is the one a flow almost always means,
		// so it is reported on its own rather than left to be searched for.
		if active == nil && s.Active && s.Graphical() {
			active = row
		}
	}

	return nodeapi.One("out", &nodeapi.Msg{Payload: map[string]any{
		"sessions": rows,
		"count":    len(rows),
		"active":   active,
	}}), nil
}

// sessionPayload renders one session for a flow. Locked is reported alongside
// lock_known, because a host that does not track locking and a screen that is
// not locked are different answers and a flow acting on the difference has to
// be able to tell them apart. A field the host does not report is left out
// rather than sent as a zero that would read as a real value.
func sessionPayload(s automation.SessionInfo) map[string]any {
	row := map[string]any{
		"id":         s.ID,
		"user":       s.User,
		"type":       s.Type,
		"class":      s.Class,
		"state":      s.State,
		"active":     s.Active,
		"remote":     s.Remote,
		"graphical":  s.Graphical(),
		"locked":     s.Locked,
		"lock_known": s.LockKnown,
	}
	for key, value := range map[string]string{
		"seat":    s.Seat,
		"tty":     s.TTY,
		"display": s.Display,
	} {
		if value != "" {
			row[key] = value
		}
	}
	// Windows identifies an account by SID rather than by number, so there is
	// no uid to report there and a zero would read as root.
	if s.UID >= 0 {
		row["uid"] = s.UID
	}
	if !s.Since.IsZero() {
		row["since"] = s.Since.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return row
}

// sessionNode acts on one login session.
type sessionNode struct {
	sess    *automation.Session
	initErr error
	// creds resolves a stored credential reference to an account and password
	// for the credentialed unlock and login actions. It is nil when the agent
	// cannot reach the server for the store key, which the node reports rather
	// than attempting a credentialed action it cannot complete.
	creds agentnode.CredentialResolver
}

func (sessionNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeSession,
		Folder:      folder,
		Label:       "Session",
		Description: "Unlocks, logs into, locks, switches, or ends a login session on this agent host",
		Icon:        "las la-user-lock",
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
				Default: string(automation.SessionUnlock),
				Options: []nodeapi.Option{
					{Value: string(automation.SessionUnlock), Label: "Unlock the screen (with a credential)"},
					{Value: string(automation.SessionLogin), Label: "Log in: switch and unlock (with a credential)"},
					{Value: string(automation.SessionLock), Label: "Lock the screen"},
					{Value: string(automation.SessionActivate), Label: "Switch to this session (no unlock)"},
					{Value: string(automation.SessionGreeter), Label: "Show the login screen"},
					{Value: string(automation.SessionLogout), Label: "Log out"},
				},
				Help: "Unlock and Log in prove the account's password from a stored credential, then " +
					"dismiss the lock. The password is never a bypass: a wrong one fails. On macOS the " +
					"console's lock screen is unlocked by typing into it; a session in the background — " +
					"or an account nobody has signed in — is reached through the login window instead: " +
					"the console switches there, the credentials are typed, and the session comes back " +
					"unlocked. A root agent flips the login window to its name-and-password mode for " +
					"the sign-in and restores the setting after, and types through helpers it starts " +
					"inside each session. Lock, Switch, Show the login screen, and Log out need no " +
					"credential. Linux reads the password database as root and unlocks through logind; " +
					"macOS verifies against Directory Services and needs the agent binary trusted for " +
					"Accessibility. Windows queues the stored credential into the karakuri credential " +
					"provider — a SYSTEM agent installs it beside itself at startup, DLL and " +
					"registration included, and keeps it current across upgrades — and winlogon " +
					"submits the credential on the secure desktop; where the OS declines the " +
					"auto-submit, a helper the agent starts inside the session types the password as " +
					"the fallback. Windows shows whichever sign-in option was used last, so unlock " +
					"once by hand with the password if the lock screen is asking for a PIN. Enroll " +
					"credentials with `karakuri-agent credential set`.",
			},
			{
				ID:          "credential",
				Label:       "Credential",
				Type:        flow.FieldString,
				Placeholder: "alice",
				Help: "Name of a credential stored on the agent with `karakuri-agent credential set`, " +
					"used by Unlock and Log in. The account it names is the one whose session is acted " +
					"on; the other actions ignore it.",
			},
			{
				ID:      "target",
				Label:   "Session",
				Type:    flow.FieldSelect,
				Default: targetActive,
				Options: []nodeapi.Option{
					{Value: targetActive, Label: "The active desktop session"},
					{Value: targetUser, Label: "The session of a named user"},
					{Value: targetID, Label: "A session by id"},
					{Value: targetGreeter, Label: "The login screen's own session"},
				},
				Help: "Ids come from the Session list node and are not stable across a reboot, " +
					"so naming the user is usually what a flow wants.",
			},
			{
				ID:          "user",
				Label:       "User",
				Type:        flow.FieldString,
				Placeholder: "alice",
				ShowIf:      &flow.ShowIf{Field: "target", Equals: targetUser},
				Help:        "Matched against the account name. A user with several sessions resolves to their active one.",
			},
			{
				ID:          "session_id",
				Label:       "Session id",
				Type:        flow.FieldString,
				Placeholder: "2",
				ShowIf:      &flow.ShowIf{Field: "target", Equals: targetID},
			},
		},
	}
}

// sessionConfig is the decoded action configuration.
type sessionConfig struct {
	Action     string `json:"action"`
	Target     string `json:"target"`
	User       string `json:"user"`
	SessionID  string `json:"session_id"`
	Credential string `json:"credential"`
}

// Execute resolves the session the flow named and performs the action on it.
func (n *sessionNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg sessionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	action := automation.SessionAction(strings.TrimSpace(cfg.Action))
	if action == "" {
		action = automation.SessionUnlock
	}

	sessions, err := n.sess.Sessions.List(ctx)
	if err != nil {
		return nil, err
	}

	// Unlock and Log in are credentialed: the target is the credential's own
	// account, and the password is proven before anything happens. Everything
	// else is a password-free action on the session the config names.
	if action == automation.SessionUnlock || action == automation.SessionLogin {
		target, user, err := n.credentialedAction(ctx, cfg, action, sessions)
		if err != nil {
			return nil, err
		}
		ec.Logger().Info("credentialed session action",
			zap.String("action", string(action)), zap.String("session", target), zap.String("user", user))
		msg := agentnode.PrimaryInput(inputs)
		return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{
			"session_action": string(action),
			"session_id":     target,
			"session_user":   user,
		})), nil
	}

	target, err := resolveSession(sessions, cfg)
	if err != nil {
		return nil, err
	}
	ec.Logger().Info("session action requested",
		zap.String("action", string(action)),
		zap.String("session", target.ID),
		zap.String("user", target.User))
	if err := n.sess.Sessions.Act(ctx, target, action); err != nil {
		return nil, err
	}

	msg := agentnode.PrimaryInput(inputs)
	return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{
		"session_action": string(action),
		"session_id":     target.ID,
		"session_user":   target.User,
	})), nil
}

// credentialedAction resolves the stored credential, finds that account's
// session, and performs the credentialed unlock or login on it. The password is
// zeroed the moment it has been used. It returns the acted-on session id and
// user for the emitted message.
func (n *sessionNode) credentialedAction(ctx context.Context, cfg sessionConfig, action automation.SessionAction, sessions []automation.SessionInfo) (string, string, error) {
	if n.creds == nil {
		return "", "", fmt.Errorf("credentialed unlock is not configured: enroll a credential with " +
			"`karakuri-agent credential set` and make sure the agent can reach the server")
	}
	ref := strings.TrimSpace(cfg.Credential)
	if ref == "" {
		return "", "", fmt.Errorf("no credential named for a %s action", action)
	}

	user, password, err := n.creds(ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("resolving credential %q: %w", ref, err)
	}
	defer func() {
		password = strings.Repeat("\x00", len(password))
		_ = password
	}()

	// The session to act on is the credential's own account's session. A login
	// with no match is handed to the platform backend naming just the account;
	// backends that cannot create a fresh graphical session return unsupported.
	target, err := resolveSession(sessions, sessionConfig{Target: targetUser, User: user})
	if err != nil && action == automation.SessionLogin {
		target = automation.SessionInfo{User: user}
		err = nil
	}
	if err != nil {
		return "", "", err
	}

	err = n.sess.Sessions.Login(ctx, automation.LoginRequest{
		User:   user,
		Secret: password,
		Target: target,
		Action: action,
	})
	if err != nil {
		return "", "", err
	}
	return target.ID, user, nil
}

// resolveSession picks the session the configuration names. Nothing here
// guesses: a target that matches nothing is an error naming what was on the
// host, because unlocking the wrong screen is worse than not unlocking one.
func resolveSession(sessions []automation.SessionInfo, cfg sessionConfig) (automation.SessionInfo, error) {
	var candidates []automation.SessionInfo
	switch cfg.Target {
	case targetID:
		id := strings.TrimSpace(cfg.SessionID)
		if id == "" {
			return automation.SessionInfo{}, fmt.Errorf("no session id to act on")
		}
		for _, s := range sessions {
			if s.ID == id {
				return s, nil
			}
		}
		return automation.SessionInfo{}, fmt.Errorf("no session %s on this host; %s", id, summarize(sessions))

	case targetUser:
		name := strings.TrimSpace(cfg.User)
		if name == "" {
			return automation.SessionInfo{}, fmt.Errorf("no user to act on")
		}
		for _, s := range sessions {
			// Windows names its accounts domain-qualified; the bare account
			// name is what a caller matching a user means.
			if strings.EqualFold(s.User, name) || strings.EqualFold(autoapi.AccountName(s.User), name) {
				candidates = append(candidates, s)
			}
		}
		if len(candidates) == 0 {
			return automation.SessionInfo{}, fmt.Errorf("%s has no session on this host; %s", name, summarize(sessions))
		}

	case targetGreeter:
		for _, s := range sessions {
			if s.Class == automation.SessionClassGreeter {
				candidates = append(candidates, s)
			}
		}
		if len(candidates) == 0 {
			// The login screen is not always a session of its own, and the
			// backends that can raise one anyway do it from a seat rather than
			// from a session, so an empty target is a legitimate answer here.
			return automation.SessionInfo{Class: automation.SessionClassGreeter}, nil
		}

	default:
		// Every session somebody is signed into, not only the graphical ones:
		// the ranking below prefers a desktop, and on a host that reports no
		// graphical session the useful failure is the one the action itself
		// gives rather than one from here.
		for _, s := range sessions {
			if s.Class == automation.SessionClassUser {
				candidates = append(candidates, s)
			}
		}
		if len(candidates) == 0 {
			return automation.SessionInfo{}, fmt.Errorf("nobody is signed in on this host; %s", summarize(sessions))
		}
	}

	return bestSession(candidates), nil
}

// bestSession ranks the sessions that matched. A desktop beats everything else:
// a user signed in has a graphical session and a service-manager session, and
// only one of those has a screen to lock — logind refuses the other with
// "session does not support lock screen", which reads like the action is
// unsupported rather than like the wrong session was picked. Among desktops the
// active one wins, then the most recently started.
func bestSession(candidates []automation.SessionInfo) automation.SessionInfo {
	best := candidates[0]
	for _, s := range candidates[1:] {
		if betterSession(s, best) {
			best = s
		}
	}
	return best
}

// betterSession reports whether a is the more likely session of the two.
func betterSession(a, b automation.SessionInfo) bool {
	if a.Graphical() != b.Graphical() {
		return a.Graphical()
	}
	if a.Active != b.Active {
		return a.Active
	}
	return a.Since.After(b.Since)
}

// summarize names what was on the host, so a target that matched nothing says
// what it could have matched instead.
func summarize(sessions []automation.SessionInfo) string {
	if len(sessions) == 0 {
		return "it reports no sessions at all"
	}
	seen := make([]string, 0, len(sessions))
	for _, s := range sessions {
		who := s.User
		if who == "" {
			who = s.Class
		}
		seen = append(seen, fmt.Sprintf("%s (%s)", s.ID, who))
	}
	return "it has " + strings.Join(seen, ", ")
}
