// Package dashlive holds each dashboard's runtime display state: which
// placements are hidden and why, the page a command moved viewers to, the
// alerts on screen, a dim override, and content pushed into placements. It
// is what a flow changes when it acts on a dashboard that is already open.
//
// The state lives in memory and is not persisted. A restart returns every
// dashboard to its authored start state, which is the honest answer for a
// screen: a lock or an alert outliving the process that set it would be one
// nobody remembers setting.
package dashlive

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/docs"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
)

// Command actions.
const (
	ActionShow         = "show"
	ActionHide         = "hide"
	ActionPage         = "page"
	ActionNextPage     = "next_page"
	ActionPreviousPage = "previous_page"
	ActionAlert        = "alert"
	ActionClearAlert   = "clear_alert"
	ActionDim          = "dim"
	ActionUndim        = "undim"
	ActionReload       = "reload"
	ActionContent      = "content"
	ActionVariable     = "variable"
)

// Actions lists every command action in the order editors offer them.
var Actions = []string{
	ActionShow, ActionHide, ActionPage, ActionNextPage, ActionPreviousPage,
	ActionAlert, ActionClearAlert, ActionDim, ActionUndim, ActionReload, ActionContent, ActionVariable,
}

// Limits on what one dashboard's state may hold.
const (
	maxAlerts       = 5
	maxLocks        = 16
	maxTitleBytes   = 200
	maxMessageBytes = 4096
	maxContentBytes = 64 << 10
	maxAlertSeconds = 86400
	// defaultAlertSeconds is how long an alert stays when the command names
	// no duration.
	defaultAlertSeconds = 10
	// StartLock is the lock a placement authored hidden on start begins with.
	StartLock = "start"
	// defaultLock is the lock show and hide use when the command names none.
	defaultLock = "command"
)

var lockRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// Alert levels and styles.
var (
	alertLevels = []string{"info", "success", "warning", "error"}
	alertStyles = []string{"toast", "fullscreen"}
)

// Alert is one message shown over a dashboard.
type Alert struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
	Level   string `json:"level"`
	Style   string `json:"style"`
	// Speak asks viewers to read the alert aloud when it arrives.
	Speak     bool       `json:"speak,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Content is a value pushed into one placement. HTML is filled in when the
// value is a Markdown string, rendered with the documentation renderer,
// which escapes raw markup.
type Content struct {
	Value     json.RawMessage `json:"value"`
	Format    string          `json:"format,omitempty"`
	HTML      string          `json:"html,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// State is one dashboard's runtime display state. A placement present in
// Locks has been commanded at least once; its slice, possibly empty, replaces
// the authored start state. A placement absent from it starts hidden only if
// it was authored that way. A variable absent from Variables is at its default.
type State struct {
	Seq       int64               `json:"seq"`
	Locks     map[string][]string `json:"locks"`
	Page      string              `json:"page,omitempty"`
	Alerts    []Alert             `json:"alerts"`
	Dim       *int                `json:"dim,omitempty"`
	Content   map[string]Content  `json:"content"`
	Variables map[string]string   `json:"variables"`
}

// Message is what the live topic carries: the whole state after a change,
// and the action when it is one a viewer must react to beyond redrawing.
type Message struct {
	State *State `json:"state"`
	Event string `json:"event,omitempty"`
}

// Command is one action on a dashboard's display.
type Command struct {
	Action  string   `json:"action" description:"show, hide, page, next_page, previous_page, alert, clear_alert, dim, undim, reload, or content."`
	Targets []string `json:"targets,omitempty" description:"Placement IDs for show, hide, and content."`
	Lock    string   `json:"lock,omitempty" description:"Lock name for show and hide; defaults to command."`
	Force   bool     `json:"force,omitempty" description:"Clear every lock on show, including start."`
	Page    string   `json:"page,omitempty" description:"Page ID, title, or 1-based index for page."`
	Title   string   `json:"title,omitempty" description:"Alert title, up to 200 bytes."`
	Message string   `json:"message,omitempty" description:"Alert message, up to 4 KiB."`
	Level   string   `json:"level,omitempty" description:"Alert level: info, success, warning, or error."`
	Style   string   `json:"style,omitempty" description:"Alert style: toast or fullscreen."`
	Speak   bool     `json:"speak,omitempty" description:"Read the alert aloud on each viewer that receives it."`
	// Duration is a pointer because zero is a request: keep the alert until
	// it is cleared.
	Duration *int            `json:"duration,omitempty" description:"Alert seconds, 0 to 86400; 0 keeps it until cleared. Defaults to 10."`
	AlertID  string          `json:"alert_id,omitempty" description:"Alert to clear; empty clears every alert."`
	DimLevel *int            `json:"dim_level,omitempty" description:"Dim level 0 to 100 for dim; defaults to 100."`
	Value    json.RawMessage `json:"value,omitempty" description:"Content value for content; null clears it. For variable, a string naming an option value or label; empty returns to the default."`
	Format   string          `json:"format,omitempty" description:"Content format: text, markdown, or json."`
	Variable string          `json:"variable,omitempty" description:"Variable name for variable."`
}

// Result reports what a command did.
type Result struct {
	Seq     int64  `json:"seq"`
	AlertID string `json:"alert_id,omitempty"`
}

// Layout is the part of a served layout commands are checked against.
type Layout struct {
	// Hidden maps every served placement ID to whether it starts hidden.
	Hidden    map[string]bool
	Pages     []models.DashboardPage
	Variables []models.DashboardVariable
}

// Store holds every dashboard's state.
type Store struct {
	mu      sync.Mutex
	states  map[uuid.UUID]*State
	timers  map[string]*time.Timer
	lastSeq int64

	publish func(topic, msgType string, payload any)
	layout  func(uuid.UUID) (*Layout, error)
	now     func() time.Time
}

// S is the server's store, publishing on the in-process broker.
var S = New(mq.B.Publish, ServedLayout)

// New returns a store that publishes with publish and checks commands
// against the layouts layout returns.
func New(publish func(topic, msgType string, payload any), layout func(uuid.UUID) (*Layout, error)) *Store {
	return &Store{
		states:  make(map[uuid.UUID]*State),
		timers:  make(map[string]*time.Timer),
		publish: publish,
		layout:  layout,
		now:     time.Now,
	}
}

// ServedLayout loads what viewers of a dashboard are served: the deployed
// version, or the draft when it has never been deployed. A deployed version
// that cannot be read is an error rather than a fall back to the draft, so a
// command is never checked against content viewers do not see.
func ServedLayout(dashboardID uuid.UUID) (*Layout, error) {
	d, err := models.LookupDashboard(dashboardID.String())
	if err != nil {
		return nil, err
	}
	layout := &Layout{Hidden: map[string]bool{}}
	if d.DeployedVersion > 0 {
		version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
		if err != nil {
			return nil, err
		}
		snap, err := models.ParseDashboardSnapshot(version.Snapshot)
		if err != nil {
			return nil, err
		}
		for _, w := range snap.Widgets {
			layout.Hidden[w.ID.String()] = w.Display != nil && w.Display.Hidden
		}
		layout.Pages = snap.PageRows(d.ID)
		if snap.Display != nil {
			layout.Variables = snap.Display.Variables
		}
		return layout, nil
	}
	widgets, err := models.ListDashboardWidgets(d.ID)
	if err != nil {
		return nil, err
	}
	for _, w := range widgets {
		layout.Hidden[w.ID.String()] = w.Display != nil && w.Display.Hidden
	}
	pages, err := models.ListDashboardPages(d.ID)
	if err != nil {
		return nil, err
	}
	layout.Pages = pages
	if d.Display != nil {
		layout.Variables = d.Display.Variables
	}
	return layout, nil
}

func newState() *State {
	return &State{Locks: map[string][]string{}, Alerts: []Alert{}, Content: map[string]Content{}, Variables: map[string]string{}}
}

// Get returns a copy of a dashboard's current state.
func (s *Store) Get(dashboardID uuid.UUID) *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[dashboardID]
	if !ok {
		return newState()
	}
	return st.clone()
}

// Forget drops a deleted dashboard's state and stops its alert timers.
func (s *Store) Forget(dashboardID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[dashboardID]; ok {
		for _, a := range st.Alerts {
			s.stopTimer(a.ID)
		}
	}
	delete(s.states, dashboardID)
}

// clone copies a state deeply enough that the caller may serialize it
// without holding the store lock.
func (st *State) clone() *State {
	out := &State{
		Seq:       st.Seq,
		Locks:     make(map[string][]string, len(st.Locks)),
		Page:      st.Page,
		Alerts:    append([]Alert{}, st.Alerts...),
		Content:   make(map[string]Content, len(st.Content)),
		Variables: make(map[string]string, len(st.Variables)),
	}
	for k, v := range st.Variables {
		out.Variables[k] = v
	}
	for k, v := range st.Locks {
		out.Locks[k] = append([]string{}, v...)
	}
	for k, v := range st.Content {
		out.Content[k] = v
	}
	if st.Dim != nil {
		level := *st.Dim
		out.Dim = &level
	}
	return out
}

func invalid(format string, args ...any) error {
	return apperrors.ErrValidation.WithMessage(fmt.Sprintf(format, args...))
}

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if a == value {
			return true
		}
	}
	return false
}

// Apply validates a command against the dashboard's served layout, applies
// it, and publishes the new state. An invalid command changes nothing.
func (s *Store) Apply(dashboardID uuid.UUID, cmd Command) (*Result, error) {
	layout, err := s.layout(dashboardID)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found or its deployed layout cannot be read")
	}
	if err := checkTargets(cmd, layout); err != nil {
		return nil, err
	}

	s.mu.Lock()
	st, ok := s.states[dashboardID]
	if !ok {
		st = newState()
	}
	// Work on a copy so a command that fails partway leaves nothing behind.
	next := st.clone()
	event, alertID, err := s.mutate(dashboardID, next, cmd, layout)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	next.Seq = s.nextSeq()
	s.states[dashboardID] = next
	out := next.clone()
	s.mu.Unlock()

	s.publish(dashscope.LiveTopic(dashboardID.String()), "state", Message{State: out, Event: event})
	return &Result{Seq: out.Seq, AlertID: alertID}, nil
}

// nextSeq returns a sequence number above every earlier one, including those
// of a previous process: it starts from the wall clock in milliseconds, so a
// viewer that kept the last number across a server restart still accepts
// the new state.
func (s *Store) nextSeq() int64 {
	seq := s.now().UnixMilli()
	if seq <= s.lastSeq {
		seq = s.lastSeq + 1
	}
	s.lastSeq = seq
	return seq
}

// checkTargets refuses placement IDs the served layout does not hold, so a
// typo cannot grow the state.
func checkTargets(cmd Command, layout *Layout) error {
	switch cmd.Action {
	case ActionShow, ActionHide, ActionContent:
		if len(cmd.Targets) == 0 {
			return invalid("%s needs at least one target placement", cmd.Action)
		}
		for _, t := range cmd.Targets {
			if _, ok := layout.Hidden[t]; !ok {
				return invalid("placement %q is not on the served layout of this dashboard", t)
			}
		}
	}
	return nil
}

// mutate applies one command to a state the caller owns. It returns the
// event viewers must react to, if any, and the ID of a created alert.
func (s *Store) mutate(dashboardID uuid.UUID, st *State, cmd Command, layout *Layout) (string, string, error) {
	switch cmd.Action {
	case ActionShow, ActionHide:
		lock := cmd.Lock
		if lock == "" {
			lock = defaultLock
		}
		if !lockRe.MatchString(lock) {
			return "", "", invalid("lock names are 1 to 64 letters, digits, underscores, hyphens, or dots")
		}
		for _, target := range cmd.Targets {
			locks, touched := st.Locks[target]
			if !touched {
				locks = []string{}
				if layout.Hidden[target] {
					locks = []string{StartLock}
				}
			}
			locks = applyLock(locks, cmd.Action, lock, cmd.Force)
			if len(locks) > maxLocks {
				return "", "", invalid("a placement holds at most %d locks", maxLocks)
			}
			st.Locks[target] = locks
		}
		return "", "", nil

	case ActionPage:
		page, err := resolvePage(layout.Pages, cmd.Page)
		if err != nil {
			return "", "", err
		}
		st.Page = page
		return ActionPage, "", nil

	case ActionNextPage, ActionPreviousPage:
		if len(layout.Pages) == 0 {
			return "", "", invalid("dashboard has no pages")
		}
		return cmd.Action, "", nil

	case ActionReload:
		return ActionReload, "", nil

	case ActionDim:
		level := 100
		if cmd.DimLevel != nil {
			level = *cmd.DimLevel
		}
		if level < 0 || level > 100 {
			return "", "", invalid("dim_level must be 0 to 100")
		}
		st.Dim = &level
		return "", "", nil

	case ActionUndim:
		st.Dim = nil
		return "", "", nil

	case ActionAlert:
		id, err := s.addAlert(dashboardID, st, cmd)
		return "", id, err

	case ActionClearAlert:
		kept := st.Alerts[:0]
		for _, a := range st.Alerts {
			if cmd.AlertID == "" || a.ID == cmd.AlertID {
				s.stopTimer(a.ID)
				continue
			}
			kept = append(kept, a)
		}
		st.Alerts = kept
		return "", "", nil

	case ActionContent:
		content, clear, err := buildContent(cmd, s.now())
		if err != nil {
			return "", "", err
		}
		for _, target := range cmd.Targets {
			if clear {
				delete(st.Content, target)
			} else {
				st.Content[target] = content
			}
		}
		return "", "", nil

	case ActionVariable:
		if err := applyVariable(st, cmd, layout); err != nil {
			return "", "", err
		}
		return ActionVariable, "", nil
	}
	return "", "", invalid("action must be one of %s", strings.Join(Actions, ", "))
}

// applyVariable sets a variable's dashboard-wide value to the option the command
// names, or returns it to its default for an empty value.
func applyVariable(st *State, cmd Command, layout *Layout) error {
	var variable *models.DashboardVariable
	index := 0
	for i := range layout.Variables {
		if layout.Variables[i].Name == cmd.Variable {
			variable, index = &layout.Variables[i], i
			break
		}
	}
	if variable == nil {
		return invalid("variable %q is not defined on the served layout of this dashboard", cmd.Variable)
	}
	if !variable.Pickable() {
		return invalid("variable %q is a constant", variable.Name)
	}
	if variable.Follows() {
		return invalid("variable %q follows %s; set that entity instead", variable.Name, variable.Query.EntityID)
	}
	// A query's options are resolved here rather than with the layout, so a
	// show or alert command never waits on Home Assistant. A query that
	// refers to earlier variables resolves with them, since its options are
	// the union over theirs; alone it would run with the references unfilled.
	toResolve := []models.DashboardVariable{*variable}
	if chained(*variable) {
		toResolve = layout.Variables[:index+1]
	}
	resolved, _ := dashvars.Resolve(toResolve)
	variable = &resolved[len(resolved)-1]
	var ref string
	if len(cmd.Value) > 0 && string(cmd.Value) != "null" {
		if err := json.Unmarshal(cmd.Value, &ref); err != nil {
			return invalid("variable value must be a string")
		}
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		delete(st.Variables, variable.Name)
		return nil
	}
	option, ok := variable.Resolve(ref)
	if !ok {
		return invalid("%q is not an option of variable %q", ref, variable.Name)
	}
	st.Variables[variable.Name] = option.Value
	return nil
}

// chained reports whether a variable is a time-series query whose text,
// selector, or range depends on other variables.
func chained(v models.DashboardVariable) bool {
	if v.Kind() != models.VariableTypeQuery || v.Query == nil || !dashvars.IsTimeSeriesSource(v.Query.Source) {
		return false
	}
	return v.Query.RangeVariable != "" || len(dashvars.QueryReferences(v.Query)) > 0
}

// applyLock adds or releases one lock. A forced show releases them all.
func applyLock(locks []string, action, lock string, force bool) []string {
	if action == ActionShow {
		if force {
			return []string{}
		}
		kept := locks[:0:0]
		for _, l := range locks {
			if l != lock {
				kept = append(kept, l)
			}
		}
		return kept
	}
	for _, l := range locks {
		if l == lock {
			return locks
		}
	}
	return append(append([]string{}, locks...), lock)
}

// resolvePage finds a page by ID, by title ignoring case, or by 1-based
// index, in that order. An empty reference returns to the first page.
func resolvePage(pages []models.DashboardPage, ref string) (string, error) {
	if len(pages) == 0 {
		return "", invalid("dashboard has no pages")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return pages[0].ID.String(), nil
	}
	for _, p := range pages {
		if p.ID.String() == ref {
			return ref, nil
		}
	}
	for _, p := range pages {
		if strings.EqualFold(p.Title, ref) {
			return p.ID.String(), nil
		}
	}
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 && n <= len(pages) {
		return pages[n-1].ID.String(), nil
	}
	return "", invalid("page %q matches no page ID, title, or index on this dashboard", ref)
}

// addAlert validates and appends an alert, dropping the oldest beyond the
// cap and arming its expiry.
func (s *Store) addAlert(dashboardID uuid.UUID, st *State, cmd Command) (string, error) {
	title := strings.TrimSpace(cmd.Title)
	message := strings.TrimSpace(cmd.Message)
	if title == "" && message == "" {
		return "", invalid("an alert needs a title or a message")
	}
	if len(title) > maxTitleBytes || len(message) > maxMessageBytes {
		return "", invalid("alert title is limited to %d bytes and message to %d bytes", maxTitleBytes, maxMessageBytes)
	}
	level := cmd.Level
	if level == "" {
		level = "info"
	}
	if !oneOf(level, alertLevels) {
		return "", invalid("alert level must be one of %s", strings.Join(alertLevels, ", "))
	}
	style := cmd.Style
	if style == "" {
		style = "toast"
	}
	if !oneOf(style, alertStyles) {
		return "", invalid("alert style must be toast or fullscreen")
	}
	seconds := defaultAlertSeconds
	if cmd.Duration != nil {
		seconds = *cmd.Duration
	}
	if seconds < 0 || seconds > maxAlertSeconds {
		return "", invalid("alert duration must be 0 to %d seconds", maxAlertSeconds)
	}

	now := s.now()
	alert := Alert{ID: uuid.NewString(), Title: title, Message: message, Level: level, Style: style, Speak: cmd.Speak, CreatedAt: now}
	if seconds > 0 {
		expires := now.Add(time.Duration(seconds) * time.Second)
		alert.ExpiresAt = &expires
		id := alert.ID
		s.timers[id] = time.AfterFunc(time.Duration(seconds)*time.Second, func() { s.expire(dashboardID, id) })
	}
	st.Alerts = append(st.Alerts, alert)
	for len(st.Alerts) > maxAlerts {
		s.stopTimer(st.Alerts[0].ID)
		st.Alerts = st.Alerts[1:]
	}
	return alert.ID, nil
}

// expire removes one alert when its duration runs out and publishes the
// change. An alert already cleared or dropped is ignored.
func (s *Store) expire(dashboardID uuid.UUID, alertID string) {
	s.mu.Lock()
	delete(s.timers, alertID)
	st, ok := s.states[dashboardID]
	if !ok {
		s.mu.Unlock()
		return
	}
	next := st.clone()
	kept := next.Alerts[:0]
	found := false
	for _, a := range next.Alerts {
		if a.ID == alertID {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		s.mu.Unlock()
		return
	}
	next.Alerts = kept
	next.Seq = s.nextSeq()
	s.states[dashboardID] = next
	out := next.clone()
	s.mu.Unlock()
	s.publish(dashscope.LiveTopic(dashboardID.String()), "state", Message{State: out})
}

// stopTimer cancels an alert's expiry. The caller holds the store lock.
func (s *Store) stopTimer(alertID string) {
	if t, ok := s.timers[alertID]; ok {
		t.Stop()
		delete(s.timers, alertID)
	}
}

// buildContent validates a content command. A JSON null clears the content.
func buildContent(cmd Command, now time.Time) (Content, bool, error) {
	raw := cmd.Value
	if len(raw) == 0 || string(raw) == "null" {
		return Content{}, true, nil
	}
	if len(raw) > maxContentBytes {
		return Content{}, false, invalid("content is limited to %d bytes of JSON", maxContentBytes)
	}
	if !json.Valid(raw) {
		return Content{}, false, invalid("content value must be JSON")
	}
	format := cmd.Format
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "markdown" && format != "json" {
		return Content{}, false, invalid("content format must be text, markdown, or json")
	}
	content := Content{Value: append(json.RawMessage{}, raw...), Format: format, UpdatedAt: now}
	if format == "markdown" {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return Content{}, false, invalid("markdown content must be a string")
		}
		content.HTML = docs.Render(text)
	}
	return content, false, nil
}
