package dashops

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"gorm.io/gorm"
)

// Display setting limits. The page interval floor keeps a rotation from
// flipping faster than a widget can load; the shift ceiling keeps the moved
// content from leaving the padding most dashboards have.
const (
	minPageInterval    = 5
	maxPageInterval    = 3600
	maxPixelShift      = 16
	maxIdleAfter       = 86400
	maxAnimationMS     = 5000
	maxConditions      = 16
	maxPlacementName   = 120
	maxPageTitleBytes  = 120
	maxVariables       = 8
	maxVariableOptions = 50
	maxVariableValue   = 200
	maxVariableLabel   = 120
	// maxVariableDescription bounds a picker's tooltip.
	maxVariableDescription = 500
	maxCues                = 8
	maxBorderWidth         = 16
	maxTileRadius          = 64
	maxColorBytes          = 64
)

// VariableDisplays lists where a variable's picker may be drawn.
var VariableDisplays = []string{
	models.VariableDisplayAbove, models.VariableDisplayLabelHidden,
	models.VariableDisplayMenu, models.VariableDisplayHidden,
}

// variableNameRe matches a variable name, the part ${name} references carry.
var variableNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// VariableOperators lists the comparisons a variable condition may use. A variable
// value is a word from a list, so the numeric comparisons do not apply.
var VariableOperators = []string{"equals", "not_equals", "contains"}

// clockRe matches a 24-hour HH:MM time.
var clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Animations lists the enter and exit animations a placement may name.
var Animations = []string{"none", "fade", "slide-up", "slide-down", "slide-left", "slide-right", "zoom"}

// CueAnimations lists the motions a status cue's border may take.
var CueAnimations = []string{"static", "pulse", "blink", "breathe", "comet", "ants"}

// validColor accepts a value written into a style attribute. It is not a CSS
// parser: the browser drops a color it cannot read, so this only keeps a value
// from closing the declaration or loading a resource.
func validColor(c string) bool {
	if len(c) > maxColorBytes {
		return false
	}
	if strings.ContainsAny(c, ";{}<>\\\"'") {
		return false
	}
	return !strings.Contains(strings.ToLower(c), "url(")
}

// ConditionOperators lists the comparisons a state condition may use.
var ConditionOperators = []string{"equals", "not_equals", "above", "below", "contains"}

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if a == value {
			return true
		}
	}
	return false
}

// ValidateDisplay checks a dashboard's display settings.
func ValidateDisplay(d *models.DashboardDisplay) error {
	if d == nil {
		return nil
	}
	bad := func(msg string) error { return apperrors.ErrValidation.WithMessage(msg) }
	if d.PageInterval != 0 && (d.PageInterval < minPageInterval || d.PageInterval > maxPageInterval) {
		return bad(fmt.Sprintf("display.page_interval must be 0 or %d to %d seconds", minPageInterval, maxPageInterval))
	}
	if d.PixelShift < 0 || d.PixelShift > maxPixelShift {
		return bad(fmt.Sprintf("display.pixel_shift must be 0 to %d pixels", maxPixelShift))
	}
	if n := d.NightDim; n != nil {
		if !clockRe.MatchString(n.From) || !clockRe.MatchString(n.To) {
			return bad("display.night_dim from and to must be HH:MM times")
		}
		if n.Level < 0 || n.Level > 100 {
			return bad("display.night_dim.level must be 0 to 100")
		}
	}
	if i := d.IdleDim; i != nil {
		if i.After < 1 || i.After > maxIdleAfter {
			return bad(fmt.Sprintf("display.idle_dim.after must be 1 to %d seconds", maxIdleAfter))
		}
		if i.Level < 0 || i.Level > 100 {
			return bad("display.idle_dim.level must be 0 to 100")
		}
	}
	if err := validateVariables(d.Variables); err != nil {
		return bad(err.Error())
	}
	return nil
}

// ValidateVariables checks variable definitions the way a draft write does,
// for an editor previewing one before it is saved.
func ValidateVariables(variables []models.DashboardVariable) error {
	if err := validateVariables(variables); err != nil {
		return apperrors.ErrValidation.WithMessage(err.Error())
	}
	return nil
}

// validateVariables checks a dashboard's variable definitions. They are checked on
// every write rather than only by explicit validation, because the options
// are the grant a templated binding expands to.
func validateVariables(variables []models.DashboardVariable) error {
	if len(variables) > maxVariables {
		return fmt.Errorf("display.variables holds at most %d variables", maxVariables)
	}
	names := map[string]bool{}
	for i, v := range variables {
		path := fmt.Sprintf("display.variables[%d]", i)
		if !variableNameRe.MatchString(v.Name) {
			return fmt.Errorf("%s.name must be a letter followed by up to 31 letters, digits, or underscores", path)
		}
		if names[v.Name] {
			return fmt.Errorf("%s.name %q is used by another variable", path, v.Name)
		}
		earlier := make(map[string]bool, len(names))
		for n := range names {
			earlier[n] = true
		}
		names[v.Name] = true
		if len(v.Label) > maxVariableLabel {
			return fmt.Errorf("%s.label must be at most %d bytes", path, maxVariableLabel)
		}
		if len(v.Description) > maxVariableDescription {
			return fmt.Errorf("%s.description must be at most %d bytes", path, maxVariableDescription)
		}
		if v.Display != "" && !oneOf(v.Display, VariableDisplays) {
			return fmt.Errorf("%s.display must be one of %s", path, strings.Join(VariableDisplays, ", "))
		}
		if len(v.Default) > maxVariableValue {
			return fmt.Errorf("%s.default must be at most %d bytes", path, maxVariableValue)
		}
		var err error
		switch v.Kind() {
		case models.VariableTypeCustom:
			err = validateCustomVariable(v)
		case models.VariableTypeConstant:
			if v.Value == "" || len(v.Value) > maxVariableValue {
				err = fmt.Errorf("value must be 1 to %d bytes", maxVariableValue)
			} else if len(v.Options) > 0 || v.Query != nil || v.Default != "" {
				err = fmt.Errorf("a constant takes a value and no options, query, or default")
			}
		case models.VariableTypeQuery:
			err = validateQuery(v, earlier)
		case models.VariableTypeTimeRange:
			err = validateTimeRange(v)
		case models.VariableTypeInterval:
			err = validateInterval(v, variables)
		default:
			err = fmt.Errorf("type must be custom, constant, query, time_range, or interval")
		}
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// validateCustomVariable checks a listed variable's options and default.
func validateCustomVariable(v models.DashboardVariable) error {
	if v.Value != "" || v.Query != nil {
		return fmt.Errorf("a custom variable takes options, not a value or query")
	}
	if len(v.Options) < 1 || len(v.Options) > maxVariableOptions {
		return fmt.Errorf("needs 1 to %d options", maxVariableOptions)
	}
	values := map[string]bool{}
	for j, o := range v.Options {
		if o.Value == "" || len(o.Value) > maxVariableValue {
			return fmt.Errorf("options[%d].value must be 1 to %d bytes", j, maxVariableValue)
		}
		if values[o.Value] {
			return fmt.Errorf("options[%d].value %q is listed twice", j, o.Value)
		}
		values[o.Value] = true
		if len(o.Label) > maxVariableLabel {
			return fmt.Errorf("options[%d].label must be at most %d bytes", j, maxVariableLabel)
		}
	}
	if v.Default != "" && !values[v.Default] {
		return fmt.Errorf("default %q is not one of its options", v.Default)
	}
	return nil
}

// validateTimeRange checks a time range variable's presets and default.
func validateTimeRange(v models.DashboardVariable) error {
	if v.Value != "" || v.Query != nil {
		return fmt.Errorf("a time range takes presets and a default, not a value or query")
	}
	if len(v.Options) > 30 {
		return fmt.Errorf("a time range has at most 30 presets")
	}
	for j, o := range v.Options {
		if !tsdb.ValidRangeValue(o.Value) {
			return fmt.Errorf("options[%d].value %q is not a range such as now-6h,now", j, o.Value)
		}
		if len(o.Label) > maxVariableLabel {
			return fmt.Errorf("options[%d].label must be at most %d bytes", j, maxVariableLabel)
		}
	}
	if v.Default != "" && !tsdb.ValidRangeValue(v.Default) {
		return fmt.Errorf("default %q is not a range such as now-6h,now", v.Default)
	}
	return nil
}

// validateInterval checks an interval variable's durations and auto sizing.
func validateInterval(v models.DashboardVariable, variables []models.DashboardVariable) error {
	if v.Value != "" || v.Query != nil {
		return fmt.Errorf("an interval takes durations, not a value or query")
	}
	count := len(v.Options)
	if v.Auto {
		count++
	}
	if count < 1 || count > maxVariableOptions {
		return fmt.Errorf("needs 1 to %d options", maxVariableOptions)
	}
	values := map[string]bool{}
	for j, o := range v.Options {
		if o.Value != models.AutoInterval {
			if d, err := tsdb.ParseDuration(o.Value); err != nil || d <= 0 {
				return fmt.Errorf("options[%d].value %q is not a duration such as 5m", j, o.Value)
			}
		}
		if values[o.Value] {
			return fmt.Errorf("options[%d].value %q is listed twice", j, o.Value)
		}
		values[o.Value] = true
	}
	if v.Auto {
		values[models.AutoInterval] = true
		if v.RangeVariable != "" {
			found := false
			for _, other := range variables {
				if other.Name == v.RangeVariable && other.Kind() == models.VariableTypeTimeRange {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("range_variable %q is not a time range variable of this dashboard", v.RangeVariable)
			}
		}
		if v.Steps < 0 || v.Steps > 1000 {
			return fmt.Errorf("steps must be 0 to 1000")
		}
		if v.MinInterval != "" {
			if _, err := tsdb.ParseDuration(v.MinInterval); err != nil {
				return fmt.Errorf("min_interval %q is not a duration", v.MinInterval)
			}
		}
	}
	if v.Default != "" && !values[v.Default] {
		return fmt.Errorf("default %q is not one of its options", v.Default)
	}
	return nil
}

// validateTimeSeriesQuery checks a query variable over a time-series
// connection. References must name variables defined before it.
func validateTimeSeriesQuery(v models.DashboardVariable, earlier map[string]bool) error {
	q := v.Query
	switch q.Source {
	case models.QueryPrometheusLabelValues:
		if strings.TrimSpace(q.Label) == "" {
			return fmt.Errorf("a label values query needs a label")
		}
	case models.QueryPrometheusMetrics:
	case models.QueryPrometheusQuery, models.QueryInflux, models.QuerySQL:
		if strings.TrimSpace(q.Text) == "" {
			return fmt.Errorf("query.text is required")
		}
		if len(q.Text) > 10000 {
			return fmt.Errorf("query.text must be at most 10000 bytes")
		}
	}
	if q.Source == models.QueryInflux && q.Language != "" && q.Language != tsdb.LangInfluxQL && q.Language != tsdb.LangFlux && q.Language != tsdb.LangSQL {
		return fmt.Errorf("query.language must be influxql, flux, or sql")
	}
	if q.Pattern != "" || q.EntityID != "" || q.Follow {
		return fmt.Errorf("a time-series query takes no pattern, entity_id, or follow")
	}
	for name := range dashvars.QueryReferences(q) {
		if !earlier[name] {
			return fmt.Errorf("query refers to ${%s}, which is not a variable defined before it", name)
		}
	}
	if q.RangeVariable != "" && !earlier[q.RangeVariable] {
		return fmt.Errorf("query.range_variable %q is not a variable defined before it", q.RangeVariable)
	}
	return nil
}

// validateQuery checks a query variable's shape. Whether its connection and
// entity exist is a dependency question explicit validation answers.
func validateQuery(v models.DashboardVariable, earlierNames map[string]bool) error {
	q := v.Query
	if q == nil {
		return fmt.Errorf("a query variable needs a query")
	}
	if len(v.Options) > 0 || v.Value != "" {
		return fmt.Errorf("a query variable takes its options from the query")
	}
	if _, err := uuid.Parse(q.ConnectionID); err != nil {
		return fmt.Errorf("query.connection_id must be a connection ID")
	}
	if dashvars.IsTimeSeriesSource(q.Source) {
		if err := validateTimeSeriesQuery(v, earlierNames); err != nil {
			return err
		}
	} else {
		switch q.Source {
		case models.QueryHAEntities:
			if strings.TrimSpace(q.Pattern) == "" || len(q.Pattern) > maxVariableValue {
				return fmt.Errorf("query.pattern must be 1 to %d bytes, such as person.*", maxVariableValue)
			}
			if q.EntityID != "" || q.Follow {
				return fmt.Errorf("an entity query takes a pattern, not an entity_id or follow")
			}
		case models.QueryHASelect:
			if !strings.HasPrefix(q.EntityID, "input_select.") && !strings.HasPrefix(q.EntityID, "select.") {
				return fmt.Errorf("query.entity_id must be an input_select or select entity")
			}
			if q.Pattern != "" {
				return fmt.Errorf("a select query takes an entity_id, not a pattern")
			}
		default:
			return fmt.Errorf("query.source must be %s, %s, or a time-series source", models.QueryHAEntities, models.QueryHASelect)
		}
	}
	if len(q.Regex) > maxVariableValue {
		return fmt.Errorf("query.regex must be at most %d bytes", maxVariableValue)
	}
	if q.Regex != "" {
		if _, err := regexp.Compile(q.Regex); err != nil {
			return fmt.Errorf("query.regex does not compile: %v", err)
		}
	}
	if q.Sort != "" && q.Sort != "asc" && q.Sort != "desc" {
		return fmt.Errorf("query.sort must be empty, asc, or desc")
	}
	return nil
}

// ValidateWidgetDisplay checks a placement's display settings. A draft write
// passes complete=false so a condition can be saved before its source is
// picked; explicit validation passes true and reports the missing source.
func ValidateWidgetDisplay(w *models.WidgetDisplay, complete bool) error {
	if w == nil {
		return nil
	}
	bad := func(msg string) error { return apperrors.ErrValidation.WithMessage(msg) }
	if len(w.Name) > maxPlacementName {
		return bad(fmt.Sprintf("display.name must be at most %d bytes", maxPlacementName))
	}
	if w.Match != "" && w.Match != "all" && w.Match != "any" {
		return bad("display.match must be all or any")
	}
	if w.AnimateIn != "" && !oneOf(w.AnimateIn, Animations) {
		return bad("display.animate_in must be one of " + strings.Join(Animations, ", "))
	}
	if w.AnimateOut != "" && !oneOf(w.AnimateOut, Animations) {
		return bad("display.animate_out must be one of " + strings.Join(Animations, ", "))
	}
	if w.Duration < 0 || w.Duration > maxAnimationMS {
		return bad(fmt.Sprintf("display.duration must be 0 to %d milliseconds", maxAnimationMS))
	}
	if len(w.Conditions) > maxConditions {
		return bad(fmt.Sprintf("display.conditions holds at most %d conditions", maxConditions))
	}
	for i, c := range w.Conditions {
		if err := validateCondition(c, complete); err != nil {
			return bad(fmt.Sprintf("display.conditions[%d]: %s", i, err))
		}
	}
	if err := validateTileStyle(w.Style); err != nil {
		return bad("display.style." + err.Error())
	}
	if len(w.Cues) > maxCues {
		return bad(fmt.Sprintf("display.cues holds at most %d cues", maxCues))
	}
	for i, cue := range w.Cues {
		if err := validateCue(cue, complete); err != nil {
			return bad(fmt.Sprintf("display.cues[%d].%s", i, err))
		}
	}
	return nil
}

// validateTileStyle checks a placement's style overrides.
func validateTileStyle(s *models.TileStyle) error {
	if s == nil {
		return nil
	}
	for _, f := range []struct{ name, value string }{
		{"background", s.Background}, {"border_color", s.BorderColor},
		{"text_color", s.TextColor}, {"accent_color", s.AccentColor},
	} {
		if !validColor(f.value) {
			return fmt.Errorf("%s must be a CSS color of at most %d bytes", f.name, maxColorBytes)
		}
	}
	if s.BorderWidth < 0 || s.BorderWidth > maxBorderWidth {
		return fmt.Errorf("border_width must be 0 to %d pixels", maxBorderWidth)
	}
	if s.Radius != nil && (*s.Radius < 0 || *s.Radius > maxTileRadius) {
		return fmt.Errorf("radius must be 0 to %d pixels", maxTileRadius)
	}
	return nil
}

// validateCue checks one status cue. Its conditions follow the rules of
// visibility conditions.
func validateCue(c models.StatusCue, complete bool) error {
	if c.Match != "" && c.Match != "all" && c.Match != "any" {
		return fmt.Errorf("match must be all or any")
	}
	if !validColor(c.Color) {
		return fmt.Errorf("color must be a CSS color of at most %d bytes", maxColorBytes)
	}
	if c.Animation != "" && !oneOf(c.Animation, CueAnimations) {
		return fmt.Errorf("animation must be one of %s", strings.Join(CueAnimations, ", "))
	}
	if len(c.Conditions) > maxConditions {
		return fmt.Errorf("conditions holds at most %d conditions", maxConditions)
	}
	for i, cond := range c.Conditions {
		if err := validateCondition(cond, complete); err != nil {
			return fmt.Errorf("conditions[%d]: %s", i, err)
		}
	}
	return nil
}

// validateCondition checks one condition's shape. Whether its source exists
// is a dependency question answered by explicit validation, not by a write.
func validateCondition(c models.Condition, complete bool) error {
	switch c.Kind {
	case models.ConditionState:
		switch c.Source {
		case "control":
			if complete && c.ControlID == "" {
				return fmt.Errorf("a control condition needs controlId")
			}
		case "ha":
			if complete && (c.ConnectionID == "" || c.EntityID == "") {
				return fmt.Errorf("a Home Assistant condition needs connectionId and entityId")
			}
		case "mqtt":
			if complete && c.DeviceID == "" {
				return fmt.Errorf("an MQTT condition needs deviceId")
			}
		default:
			return fmt.Errorf("source must be control, ha, or mqtt")
		}
		if !oneOf(c.Operator, ConditionOperators) {
			return fmt.Errorf("operator must be one of %s", strings.Join(ConditionOperators, ", "))
		}
	case models.ConditionVariable:
		// Whether the variable exists depends on the dashboard's display
		// settings, which explicit validation checks against.
		if complete && c.Variable == "" {
			return fmt.Errorf("a variable condition needs variable")
		}
		if c.Variable != "" && !variableNameRe.MatchString(c.Variable) {
			return fmt.Errorf("variable must be a variable name")
		}
		if !oneOf(c.Operator, VariableOperators) {
			return fmt.Errorf("operator must be one of %s", strings.Join(VariableOperators, ", "))
		}
	case models.ConditionTime:
		if !clockRe.MatchString(c.From) || !clockRe.MatchString(c.To) {
			return fmt.Errorf("from and to must be HH:MM times")
		}
		for _, d := range c.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("days must be 0 (Sunday) to 6")
			}
		}
	default:
		return fmt.Errorf("kind must be state, time, or variable")
	}
	return nil
}

// normalizeWidgetDisplay stores unset placement display settings as nil.
func normalizeWidgetDisplay(w *models.WidgetDisplay) *models.WidgetDisplay {
	if w.IsZero() {
		return nil
	}
	// An empty style object would store as {}, which a snapshot taken without
	// one would not compare equal to.
	if w.Style.IsZero() {
		w.Style = nil
	}
	return w
}

// ResolveWidgetPage turns a page ID from a request into a placement's page
// reference, rejecting one that belongs to another dashboard. An empty string
// means every page.
func ResolveWidgetPage(dashboardID uuid.UUID, raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid page ID")
	}
	page, err := models.FindDashboardPage(id)
	if err != nil || page.DashboardID != dashboardID {
		return nil, apperrors.ErrValidation.WithMessage("page not found on this dashboard")
	}
	return &id, nil
}

// validateRegion accepts an empty region, which the region layout draws in
// middle_center, or one of the named regions.
func validateRegion(region string) error {
	if region == "" || models.ValidDashboardRegion(region) {
		return nil
	}
	return apperrors.ErrValidation.WithMessage("region must be one of " + strings.Join(models.DashboardRegions, ", "))
}

// validatePageTitle checks a page title.
func validatePageTitle(title string) error {
	if strings.TrimSpace(title) == "" || len(title) > maxPageTitleBytes {
		return apperrors.ErrValidation.WithMessage(fmt.Sprintf("page title must be 1 to %d bytes", maxPageTitleBytes))
	}
	return nil
}

// AddPage appends a page to a dashboard's draft.
func AddPage(dashboard *models.Dashboard, title string) (*models.DashboardPage, error) {
	if err := validatePageTitle(title); err != nil {
		return nil, err
	}
	page := &models.DashboardPage{
		DashboardID: dashboard.ID,
		Title:       strings.TrimSpace(title),
		SortOrder:   models.NextDashboardPageOrder(dashboard.ID),
	}
	if err := db.DB.Create(page).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return page, nil
}

// PagePatch holds the optional fields UpdatePage applies.
type PagePatch struct {
	Title     *string
	SortOrder *int
}

// FindPage loads a page belonging to a dashboard.
func FindPage(dashboard *models.Dashboard, raw string) (*models.DashboardPage, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid page ID")
	}
	page, err := models.FindDashboardPage(id)
	if err != nil || page.DashboardID != dashboard.ID {
		return nil, apperrors.ErrNotFound.WithMessage("page not found on this dashboard")
	}
	return page, nil
}

// UpdatePage renames or moves a page.
func UpdatePage(dashboard *models.Dashboard, pageID string, p PagePatch) (*models.DashboardPage, error) {
	page, err := FindPage(dashboard, pageID)
	if err != nil {
		return nil, err
	}
	if p.Title != nil {
		if err := validatePageTitle(*p.Title); err != nil {
			return nil, err
		}
		page.Title = strings.TrimSpace(*p.Title)
	}
	if p.SortOrder != nil {
		page.SortOrder = *p.SortOrder
	}
	if err := db.DB.Model(page).Select("title", "sort_order", "updated_at").Updates(page).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return page, nil
}

// ReorderPages sets every listed page's position in one transaction.
func ReorderPages(dashboard *models.Dashboard, order map[uuid.UUID]int) error {
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		for id, position := range order {
			res := tx.Model(&models.DashboardPage{}).
				Where("id = ? AND dashboard_id = ?", id, dashboard.ID).
				Update("sort_order", position)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return apperrors.ErrNotFound.WithMessage("page not found on this dashboard")
			}
		}
		return nil
	})
	if err != nil {
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			return err
		}
		return apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return nil
}

// RemovePage deletes a page and moves its placements onto every page.
func RemovePage(dashboard *models.Dashboard, pageID string) error {
	page, err := FindPage(dashboard, pageID)
	if err != nil {
		return err
	}
	if err := page.Delete(); err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return nil
}

// ServedVariables returns the variables of the view a viewer, render, or
// editor is served: the draft's when draft is set or the dashboard has never
// been deployed, otherwise the deployed version's. They are not resolved.
func ServedVariables(d *models.Dashboard, draft bool) ([]models.DashboardVariable, error) {
	display := d.Display
	if !draft && d.DeployedVersion > 0 {
		version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
		if err != nil {
			return nil, apperrors.ErrNotFound.WithMessage("deployed dashboard version not found")
		}
		snap, err := models.ParseDashboardSnapshot(version.Snapshot)
		if err != nil {
			return nil, apperrors.ErrInternal.WithMessage("deployed dashboard version cannot be read")
		}
		display = snap.Display
	}
	if display == nil {
		return nil, nil
	}
	return display.Variables, nil
}

// ResolveVariableValues checks variable values a render names against the view
// it renders. It returns them as option values, resolving labels, and refuses
// an unknown variable or value and a constant, whose value is fixed.
func ResolveVariableValues(d *models.Dashboard, draft bool, values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	served, err := ServedVariables(d, draft)
	if err != nil {
		return nil, err
	}
	resolved, _ := dashvars.Resolve(served)
	display := &models.DashboardDisplay{Variables: resolved}
	out := make(map[string]string, len(values))
	for name, ref := range values {
		variable, ok := display.FindVariable(name)
		if !ok {
			return nil, apperrors.ErrValidation.WithMessage(fmt.Sprintf("variable %q is not defined on this dashboard", name))
		}
		if !variable.Pickable() {
			return nil, apperrors.ErrValidation.WithMessage(fmt.Sprintf("variable %q is a constant", name))
		}
		option, ok := variable.Resolve(ref)
		if !ok {
			return nil, apperrors.ErrValidation.WithMessage(fmt.Sprintf("%q is not an option of variable %q", ref, name))
		}
		out[name] = option.Value
	}
	return out, nil
}
