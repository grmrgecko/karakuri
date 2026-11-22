package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"gorm.io/gorm"
)

// DashboardLayoutRegions anchors placements to named regions of the screen
// instead of grid cells. A region stacks its placements in order and grows
// with them, the arrangement a smart mirror is built from.
const DashboardLayoutRegions = "regions"

// DashboardRegions lists the region names a placement may name in the region
// layout, in the order they are drawn.
var DashboardRegions = []string{
	"fullscreen_below",
	"top_bar",
	"top_left", "top_center", "top_right",
	"upper_third", "middle_center", "lower_third",
	"bottom_left", "bottom_center", "bottom_right",
	"bottom_bar",
	"fullscreen_above",
}

// ValidDashboardRegion reports whether a region name is one the region layout
// draws.
func ValidDashboardRegion(name string) bool {
	for _, r := range DashboardRegions {
		if r == name {
			return true
		}
	}
	return false
}

// DashboardDisplay holds the always-on screen settings frozen with a layout.
// A dashboard with none of them set stores nil, which keeps its snapshot
// identical to one deployed before the settings existed.
type DashboardDisplay struct {
	// PageInterval rotates pages every this many seconds; zero leaves the
	// page where a viewer or a command put it.
	PageInterval int        `json:"page_interval,omitempty" description:"Seconds between automatic page changes; 0 disables rotation."`
	NightDim     *DimWindow `json:"night_dim,omitempty" description:"Daily window during which the screen is dimmed."`
	IdleDim      *IdleDim   `json:"idle_dim,omitempty" description:"Dims the screen after a period without input."`
	// PixelShift moves the whole surface by up to this many pixels in each
	// axis every two minutes, spreading the wear of static content.
	PixelShift int `json:"pixel_shift,omitempty" description:"Maximum burn-in shift in pixels, 0 to 16."`
	// Variables are the dashboard's variables. Placement configuration names
	// them as ${name}, and the options are what the layout grants.
	Variables []DashboardVariable `json:"variables,omitempty" description:"Named variables placement configuration refers to as ${name}."`
}

// Variable types.
const (
	VariableTypeCustom   = "custom"
	VariableTypeConstant = "constant"
	VariableTypeQuery    = "query"
	// VariableTypeTimeRange is a span "from,to" that charts read. Its
	// presets are suggestions: any value that parses is accepted, because a
	// range bounds when data is read, not which data.
	VariableTypeTimeRange = "time_range"
	// VariableTypeInterval is a duration such as 5m, or auto.
	VariableTypeInterval = "interval"
)

// Variable display modes. An empty mode draws the picker above the dashboard.
const (
	VariableDisplayAbove       = "above"
	VariableDisplayLabelHidden = "label_hidden"
	VariableDisplayMenu        = "menu"
	VariableDisplayHidden      = "hidden"
)

// Query sources.
const (
	// QueryHAEntities lists the Home Assistant entities whose IDs match a
	// pattern.
	QueryHAEntities = "ha_entities"
	// QueryHASelect lists the options of an input_select or select entity.
	QueryHASelect = "ha_select"
	// QueryPrometheusLabelValues lists a label's values, optionally of the
	// series a selector matches.
	QueryPrometheusLabelValues = "prometheus_label_values"
	// QueryPrometheusMetrics lists metric names.
	QueryPrometheusMetrics = "prometheus_metrics"
	// QueryPrometheusQuery runs a PromQL instant query and lists the values
	// of one label of the result, or the series names.
	QueryPrometheusQuery = "prometheus_query"
	// QueryInflux runs an InfluxQL, Flux, or SQL query and lists its first
	// column.
	QueryInflux = "influx_query"
	// QuerySQL runs a SQL query and takes __value and __text columns, or
	// the first and second.
	QuerySQL = "sql_query"
)

// DashboardVariable is one named choice a dashboard's placements are templated
// on. Its options are a closed set because each one is a grant: a value
// outside them could name data the author never placed. A custom variable
// lists them, a constant has one, and a query has whatever its source returns
// when it is resolved, which is the only time Options is set for those types.
type DashboardVariable struct {
	Name        string `json:"name" description:"Name used in ${name} references; a letter then letters, digits, or underscores."`
	Label       string `json:"label,omitempty" description:"Label shown beside the picker."`
	Description string `json:"description,omitempty" description:"Help shown as the picker's tooltip."`
	Type        string `json:"type,omitempty" description:"custom (default), constant, query, time_range, or interval."`
	Display     string `json:"display,omitempty" description:"Where the picker is drawn: above (default), label_hidden, menu, or hidden."`
	// Options are a custom variable's values, or a query's once resolved.
	Options []VariableOption `json:"options,omitempty" description:"Values a custom variable may take."`
	// Default is the value a viewer starts on; empty, or a value a query does
	// not return, means the first option.
	Default string         `json:"default,omitempty" description:"Starting value; empty uses the first option."`
	Value   string         `json:"value,omitempty" description:"A constant variable's value."`
	Query   *VariableQuery `json:"query,omitempty" description:"Where a query variable's options come from."`
	// Auto offers an interval variable's auto option, sized from a time
	// range variable.
	Auto          bool   `json:"auto,omitempty" description:"Interval: offer auto, sized from the range variable."`
	Steps         int    `json:"steps,omitempty" description:"Interval: how many steps auto divides the range into; default 30."`
	MinInterval   string `json:"min_interval,omitempty" description:"Interval: the smallest value auto resolves to."`
	RangeVariable string `json:"range_variable,omitempty" description:"Interval: the time range variable auto is sized from."`
}

// AutoInterval is the interval value sized from a time range.
const AutoInterval = "auto"

// DefaultTimeRange is a time range variable's value when it names none.
const DefaultTimeRange = "now-6h,now"

// DefaultTimePresets are the quick ranges offered by a time range
// variable that lists none of its own.
var DefaultTimePresets = []VariableOption{
	{Value: "now-5m,now", Label: "Last 5 minutes"},
	{Value: "now-15m,now", Label: "Last 15 minutes"},
	{Value: "now-30m,now", Label: "Last 30 minutes"},
	{Value: "now-1h,now", Label: "Last 1 hour"},
	{Value: "now-3h,now", Label: "Last 3 hours"},
	{Value: "now-6h,now", Label: "Last 6 hours"},
	{Value: "now-12h,now", Label: "Last 12 hours"},
	{Value: "now-24h,now", Label: "Last 24 hours"},
	{Value: "now-2d,now", Label: "Last 2 days"},
	{Value: "now-7d,now", Label: "Last 7 days"},
	{Value: "now-30d,now", Label: "Last 30 days"},
	{Value: "now-90d,now", Label: "Last 90 days"},
	{Value: "now/d,now/d", Label: "Today"},
	{Value: "now-1d/d,now-1d/d", Label: "Yesterday"},
	{Value: "now/w,now/w", Label: "This week"},
	{Value: "now/M,now/M", Label: "This month"},
}

// VariableQuery is where a query variable's options come from.
type VariableQuery struct {
	Source       string `json:"source" description:"ha_entities, ha_select, prometheus_label_values, prometheus_metrics, prometheus_query, influx_query, or sql_query."`
	ConnectionID string `json:"connection_id" description:"Home Assistant or time-series connection ID."`
	// Label is the Prometheus label whose values become options.
	Label string `json:"label,omitempty" description:"Prometheus label for label values and query results."`
	// Match narrows Prometheus lookups to the series a selector matches.
	Match string `json:"match,omitempty" description:"Prometheus series selector, such as up{job=\"node\"}."`
	// Text is the query of a prometheus_query, influx_query, or sql_query
	// source. It may reference earlier variables.
	Text     string `json:"text,omitempty" description:"Query text; may reference earlier variables and time macros."`
	Language string `json:"language,omitempty" description:"influx_query: influxql (default), flux, or sql."`
	// RangeVariable names the time range variable the query's time macros
	// use; empty means the last 6 hours.
	RangeVariable string `json:"range_variable,omitempty" description:"Time range variable whose default the query's time macros use."`
	// Pattern matches entity IDs, with * standing for any run of characters.
	Pattern  string `json:"pattern,omitempty" description:"Entity ID pattern for ha_entities, such as person.*."`
	EntityID string `json:"entity_id,omitempty" description:"input_select or select entity for ha_select."`
	// Regex extracts the value, and the label when it has a text group, from
	// each entity ID or select option. Items it does not match are left out.
	Regex string `json:"regex,omitempty" description:"Optional regex; a value group, or the first group, becomes the value and a text group the label."`
	Sort  string `json:"sort,omitempty" description:"Sort by label: empty keeps the source order, asc, or desc."`
	// Follow makes the dashboard-wide value the select entity's state.
	Follow bool `json:"follow,omitempty" description:"For ha_select, set the dashboard-wide value from the entity's state."`
}

// Kind returns the variable's type, custom when none is stored.
func (v DashboardVariable) Kind() string {
	if v.Type == "" {
		return VariableTypeCustom
	}
	return v.Type
}

// Follows reports whether the variable's dashboard-wide value is a select
// entity's state rather than something a command sets.
func (v DashboardVariable) Follows() bool {
	return v.Kind() == VariableTypeQuery && v.Query != nil && v.Query.Source == QueryHASelect && v.Query.Follow
}

// Pickable reports whether a viewer may choose the variable's value.
func (v DashboardVariable) Pickable() bool {
	return v.Kind() != VariableTypeConstant
}

// VariableOption is one value of a variable.
type VariableOption struct {
	Value string `json:"value" description:"Value substituted for ${name}."`
	Label string `json:"label,omitempty" description:"Text substituted for ${name:text} and shown in the picker; empty uses the value."`
}

// Text returns what ${name:text} substitutes for this option.
func (o VariableOption) Text() string {
	if o.Label != "" {
		return o.Label
	}
	return o.Value
}

// DefaultOption returns the option a viewer starts on. A variable without
// options has none.
func (f DashboardVariable) DefaultOption() (VariableOption, bool) {
	if f.Kind() == VariableTypeTimeRange {
		def := f.Default
		if def == "" || !tsdb.ValidRangeValue(def) {
			def = DefaultTimeRange
		}
		if o, ok := f.Resolve(def); ok {
			return o, true
		}
		return VariableOption{Value: def}, true
	}
	for _, o := range f.Options {
		if o.Value == f.Default {
			return o, true
		}
	}
	if len(f.Options) == 0 {
		return VariableOption{}, false
	}
	return f.Options[0], true
}

// Resolve finds the option a command or render names: by value first, then
// by label ignoring case, so a flow may pass what a person reads. A time
// range variable also accepts any value that parses as a range.
func (f DashboardVariable) Resolve(ref string) (VariableOption, bool) {
	if f.Kind() == VariableTypeTimeRange {
		for _, o := range f.Options {
			if o.Value == ref || (o.Label != "" && strings.EqualFold(o.Label, ref)) {
				return o, true
			}
		}
		if tsdb.ValidRangeValue(ref) {
			return VariableOption{Value: ref}, true
		}
		return VariableOption{}, false
	}
	for _, o := range f.Options {
		if o.Value == ref {
			return o, true
		}
	}
	for _, o := range f.Options {
		if o.Label != "" && strings.EqualFold(o.Label, ref) {
			return o, true
		}
	}
	return VariableOption{}, false
}

// FindVariable returns the named variable of a display.
func (d *DashboardDisplay) FindVariable(name string) (DashboardVariable, bool) {
	if d == nil {
		return DashboardVariable{}, false
	}
	for _, f := range d.Variables {
		if f.Name == name {
			return f, true
		}
	}
	return DashboardVariable{}, false
}

// DimWindow is a daily span in the viewer's local time. A span whose start
// is later than its end runs past midnight.
type DimWindow struct {
	From  string `json:"from" description:"Start time, HH:MM."`
	To    string `json:"to" description:"End time, HH:MM."`
	Level int    `json:"level" description:"Dim level from 0 (none) to 100 (black)."`
}

// IdleDim dims a screen nobody has touched for a while.
type IdleDim struct {
	After int `json:"after" description:"Seconds without input before dimming."`
	Level int `json:"level" description:"Dim level from 0 (none) to 100 (black)."`
}

// IsZero reports whether no display setting is set.
func (d *DashboardDisplay) IsZero() bool {
	return d == nil || (d.PageInterval == 0 && d.NightDim == nil && d.IdleDim == nil && d.PixelShift == 0 && len(d.Variables) == 0)
}

// WidgetDisplay holds a placement's visibility, animation, and appearance settings.
type WidgetDisplay struct {
	// Name labels the placement for flows and the editor. Placement IDs are
	// what commands carry; the name is what a person picks them by.
	Name string `json:"name,omitempty" description:"Label shown in pickers and the editor."`
	// Hidden starts the placement under the lock "start", released by a
	// show command naming that lock or forcing.
	Hidden     bool        `json:"hidden,omitempty" description:"Whether the placement starts hidden under the lock start."`
	Match      string      `json:"match,omitempty" description:"How conditions combine: all (default) or any."`
	Conditions []Condition `json:"conditions,omitempty" description:"Conditions that must pass for the placement to show."`
	AnimateIn  string      `json:"animate_in,omitempty" description:"Enter animation: none, fade, slide-up, slide-down, slide-left, slide-right, or zoom."`
	AnimateOut string      `json:"animate_out,omitempty" description:"Exit animation, from the same set."`
	Duration   int         `json:"duration,omitempty" description:"Animation duration in milliseconds, up to 5000."`
	// Style overrides the theme's look for this placement's tile. The host
	// applies it, so it works for every widget package.
	Style *TileStyle `json:"style,omitempty" description:"Tile background, border, radius, and colors overriding the theme."`
	// Cues are animated borders drawn while their conditions pass; the first
	// passing cue draws.
	Cues []StatusCue `json:"cues,omitempty" description:"Ordered border cues; the first whose conditions pass draws."`
}

// TileStyle overrides a placement tile's theme look. Empty fields keep the
// theme.
type TileStyle struct {
	Background  string `json:"background,omitempty" description:"CSS color, or transparent to drop the tile face, border, and shadow."`
	BorderColor string `json:"border_color,omitempty" description:"CSS border color."`
	BorderWidth int    `json:"border_width,omitempty" description:"Border width in pixels, 0 to 16."`
	// Radius is a pointer because a square tile, radius 0, differs from the
	// theme's radius.
	Radius      *int   `json:"radius,omitempty" description:"Corner radius in pixels, 0 to 64; unset keeps the theme's."`
	TextColor   string `json:"text_color,omitempty" description:"CSS color widgets use for text."`
	AccentColor string `json:"accent_color,omitempty" description:"CSS color widgets use for accents."`
}

// IsZero reports whether no style field is set.
func (s *TileStyle) IsZero() bool {
	return s == nil || (s.Background == "" && s.BorderColor == "" && s.BorderWidth == 0 && s.Radius == nil &&
		s.TextColor == "" && s.AccentColor == "")
}

// StatusCue is a border drawn in a color and motion while its conditions
// pass. Its conditions take the forms visibility conditions do, and grant
// their sources the same reads.
type StatusCue struct {
	Match      string      `json:"match,omitempty" description:"How conditions combine: all (default) or any."`
	Conditions []Condition `json:"conditions,omitempty" description:"Conditions that must pass; none always passes."`
	Color      string      `json:"color,omitempty" description:"CSS border color; empty uses the theme's warning color."`
	Animation  string      `json:"animation,omitempty" description:"static (default), pulse, blink, breathe, comet, or ants."`
}

// IsZero reports whether no placement display setting is set.
func (w *WidgetDisplay) IsZero() bool {
	return w == nil || (w.Name == "" && !w.Hidden && w.Match == "" && len(w.Conditions) == 0 &&
		w.AnimateIn == "" && w.AnimateOut == "" && w.Duration == 0 && w.Style.IsZero() && len(w.Cues) == 0)
}

// AllConditions returns the visibility conditions followed by each cue's,
// which is every source and variable the placement's display reads.
func (w *WidgetDisplay) AllConditions() []Condition {
	if w == nil {
		return nil
	}
	out := append([]Condition(nil), w.Conditions...)
	for _, c := range w.Cues {
		out = append(out, c.Conditions...)
	}
	return out
}

// Condition kinds.
const (
	ConditionState    = "state"
	ConditionTime     = "time"
	ConditionVariable = "variable"
)

// Condition is one rule a placement's visibility depends on. A state
// condition names a host binding with the same fields a widget binding uses,
// so dashboard scope grants its source the way it grants a display-only row.
type Condition struct {
	Kind string `json:"kind" description:"Condition kind: state, time, or variable."`

	// Variable names the dashboard variable a variable condition compares.
	Variable string `json:"variable,omitempty" description:"Variable name for a variable condition."`

	Source       string `json:"source,omitempty" description:"State source: control, ha, or mqtt."`
	ControlID    string `json:"controlId,omitempty" description:"Control ID for a control source."`
	ConnectionID string `json:"connectionId,omitempty" description:"Home Assistant connection ID."`
	EntityID     string `json:"entityId,omitempty" description:"Home Assistant entity ID."`
	DeviceID     string `json:"deviceId,omitempty" description:"MQTT device ID."`
	// Attribute narrows the value: a Home Assistant attribute, a composite
	// control's attribute, or a device state field. Empty reads the state.
	Attribute string `json:"attribute,omitempty" description:"Attribute or device field to compare; empty compares the state."`
	Operator  string `json:"operator,omitempty" description:"equals, not_equals, above, below, or contains; a variable condition takes equals, not_equals, or contains."`
	Value     string `json:"value,omitempty" description:"Value compared against."`

	From string `json:"from,omitempty" description:"Time condition start, HH:MM."`
	To   string `json:"to,omitempty" description:"Time condition end, HH:MM."`
	Days []int  `json:"days,omitempty" description:"Weekdays the time condition applies on, 0 for Sunday; empty means every day."`
}

// DashboardPage is one page of a dashboard. Placements name the page they
// sit on, or none to appear on every page.
type DashboardPage struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Parent dashboard ID."`
	Dashboard   Dashboard `gorm:"foreignKey:DashboardID" json:"-"`
	Title       string    `gorm:"not null" json:"title" description:"Page title."`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order" description:"Position among the dashboard's pages."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID if one has not been set.
func (p *DashboardPage) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// Delete removes a page and moves its placements onto every page. A page is
// a filter over placements, not their container, so deleting one must not
// delete what was shown on it.
func (p *DashboardPage) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&DashboardWidget{}).Where("page_id = ?", p.ID).Update("page_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&DashboardPage{}, "id = ?", p.ID).Error
	})
}

// FindDashboardPage returns one page by ID.
func FindDashboardPage(id uuid.UUID) (*DashboardPage, error) {
	var page DashboardPage
	err := db.DB.Where("id = ?", id).First(&page).Error
	return &page, err
}

// ListDashboardPages returns a dashboard's pages in display order.
func ListDashboardPages(dashboardID uuid.UUID) ([]DashboardPage, error) {
	var pages []DashboardPage
	err := db.DB.Where("dashboard_id = ?", dashboardID).
		Order("sort_order ASC, created_at ASC").Find(&pages).Error
	return pages, err
}

// NextDashboardPageOrder returns the sort position a new page takes, which
// is after the last one.
func NextDashboardPageOrder(dashboardID uuid.UUID) int {
	var maxOrder *int
	db.DB.Model(&DashboardPage{}).Where("dashboard_id = ?", dashboardID).
		Select("MAX(sort_order)").Scan(&maxOrder)
	if maxOrder == nil {
		return 0
	}
	return *maxOrder + 1
}
