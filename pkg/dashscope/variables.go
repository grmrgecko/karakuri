package dashscope

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// MaxVariableCombinations bounds how many variable value combinations one
// placement may expand to. Past it the placement grants nothing from its
// bindings or conditions, rather than an unbounded walk on every derivation.
const MaxVariableCombinations = 256

// variableRefRe matches a variable reference: ${name} for the value,
// ${name:text} for the option's label, ${name:from} and ${name:to} for a
// time range's ends, and the query escaping formats raw, regex, and
// sqlstring, which outside query text insert the plain value. It matches
// frontend/src/lib/variables.ts.
var variableRefRe = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]{0,31})(?::(text|raw|regex|sqlstring|from|to))?\}`)

// VariableValues maps variable names to the option each one currently takes.
type VariableValues map[string]models.VariableOption

// Substitute replaces the variable references in one string. A reference to a
// variable without a value is left as written.
func Substitute(s string, values VariableValues) string {
	if len(values) == 0 || !strings.Contains(s, "${") {
		return s
	}
	return variableRefRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := variableRefRe.FindStringSubmatch(match)
		option, ok := values[parts[1]]
		if !ok {
			return match
		}
		switch parts[2] {
		case "text":
			return option.Text()
		case "from", "to":
			from, to, ok := tsdb.SplitRangeValue(option.Value)
			if !ok {
				return match
			}
			if parts[2] == "from" {
				return from
			}
			return to
		}
		return option.Value
	})
}

// substituteValue rewrites every string inside decoded JSON. Keys, numbers,
// and booleans are left alone, so a value can never change a config's shape.
func substituteValue(v any, values VariableValues) any {
	switch t := v.(type) {
	case string:
		if typed, ok := typedValue(t, values); ok {
			return typed
		}
		return Substitute(t, values)
	case []any:
		for i := range t {
			t[i] = substituteValue(t[i], values)
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = substituteValue(t[k], values)
		}
		return t
	}
	return v
}

// wholeRefRe matches a string that is exactly one value reference.
var wholeRefRe = regexp.MustCompile(`^\$\{([A-Za-z][A-Za-z0-9_]{0,31})\}$`)

// numberRe matches the JSON number spellings a value may take to substitute
// as a number.
var numberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// typedValue substitutes a string that is exactly one reference whose value is
// a number or a boolean as that type, so "hours": "${range}" reaches a widget
// as a number. It matches frontend/src/lib/variables.ts.
func typedValue(s string, values VariableValues) (any, bool) {
	m := wholeRefRe.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	option, ok := values[m[1]]
	if !ok {
		return nil, false
	}
	switch {
	case option.Value == "true":
		return true, true
	case option.Value == "false":
		return false, true
	case numberRe.MatchString(option.Value):
		return json.Number(option.Value), true
	}
	return nil, false
}

// SubstituteConfig applies variable values to a placement's stored config. A
// config that does not parse is returned unchanged; bindings will not parse
// from it either.
func SubstituteConfig(configJSON string, values VariableValues) string {
	if len(values) == 0 || !strings.Contains(configJSON, "${") {
		return configJSON
	}
	var decoded any
	if err := json.Unmarshal([]byte(configJSON), &decoded); err != nil {
		return configJSON
	}
	out, err := json.Marshal(substituteValue(decoded, values))
	if err != nil {
		return configJSON
	}
	return string(out)
}

// SubstituteCondition applies variable values to a state condition's source and
// comparison fields.
func SubstituteCondition(c models.Condition, values VariableValues) models.Condition {
	c.ControlID = Substitute(c.ControlID, values)
	c.ConnectionID = Substitute(c.ConnectionID, values)
	c.EntityID = Substitute(c.EntityID, values)
	c.DeviceID = Substitute(c.DeviceID, values)
	c.Attribute = Substitute(c.Attribute, values)
	c.Value = Substitute(c.Value, values)
	return c
}

// SubstituteDisplay returns a copy of a placement's display settings with
// variable values applied to its visibility and cue conditions.
func SubstituteDisplay(d *models.WidgetDisplay, values VariableValues) *models.WidgetDisplay {
	if d == nil || len(values) == 0 {
		return d
	}
	out := *d
	out.Conditions = make([]models.Condition, len(d.Conditions))
	for i, c := range d.Conditions {
		out.Conditions[i] = SubstituteCondition(c, values)
	}
	out.Cues = make([]models.StatusCue, len(d.Cues))
	for i, cue := range d.Cues {
		cue.Conditions = make([]models.Condition, len(d.Cues[i].Conditions))
		for j, c := range d.Cues[i].Conditions {
			cue.Conditions[j] = SubstituteCondition(c, values)
		}
		out.Cues[i] = cue
	}
	return &out
}

// collectReferences adds the variable names one string refers to.
func collectReferences(s string, into map[string]bool) {
	if !strings.Contains(s, "${") {
		return
	}
	for _, parts := range variableRefRe.FindAllStringSubmatch(s, -1) {
		into[parts[1]] = true
	}
}

// collectValueReferences walks decoded JSON for references.
func collectValueReferences(v any, into map[string]bool) {
	switch t := v.(type) {
	case string:
		collectReferences(t, into)
	case []any:
		for _, item := range t {
			collectValueReferences(item, into)
		}
	case map[string]any:
		for _, item := range t {
			collectValueReferences(item, into)
		}
	}
}

// PlacementReferences returns the variable names a placement's config and
// state conditions, visibility and cue, refer to, plus those its variable conditions compare.
func PlacementReferences(p models.DashboardWidget) map[string]bool {
	refs := map[string]bool{}
	if strings.Contains(p.Config, "${") {
		var decoded any
		if json.Unmarshal([]byte(p.Config), &decoded) == nil {
			collectValueReferences(decoded, refs)
		}
	}
	if p.Display != nil {
		for _, c := range p.Display.AllConditions() {
			switch c.Kind {
			case models.ConditionState:
				for _, field := range []string{c.ControlID, c.ConnectionID, c.EntityID, c.DeviceID, c.Attribute, c.Value} {
					collectReferences(field, refs)
				}
			case models.ConditionVariable:
				if c.Variable != "" {
					refs[c.Variable] = true
				}
			}
		}
	}
	return refs
}

// VariableCombinations returns every assignment of option values to the
// referenced variables that exist, in variable order. With no such variable it is
// one empty assignment, so a placement without references derives once. It
// reports false when the product exceeds MaxVariableCombinations.
func VariableCombinations(variables []models.DashboardVariable, refs map[string]bool) ([]VariableValues, bool) {
	combos := []VariableValues{{}}
	for _, f := range variables {
		// A time range bounds when data is read, not which data, so it
		// takes no part in the grant.
		if !refs[f.Name] || len(f.Options) == 0 || f.Kind() == models.VariableTypeTimeRange {
			continue
		}
		if len(combos)*len(f.Options) > MaxVariableCombinations {
			return nil, false
		}
		next := make([]VariableValues, 0, len(combos)*len(f.Options))
		for _, combo := range combos {
			for _, option := range f.Options {
				values := make(VariableValues, len(combo)+1)
				for k, v := range combo {
					values[k] = v
				}
				values[f.Name] = option
				next = append(next, values)
			}
		}
		combos = next
	}
	return combos, true
}

// placementBindings returns every binding a placement can draw under any
// variable value, from its config and its state conditions. A placement over the
// combination limit has none.
func placementBindings(p models.DashboardWidget, path string, variables []models.DashboardVariable) []Binding {
	combos, ok := VariableCombinations(variables, PlacementReferences(p))
	if !ok {
		return nil
	}
	var out []Binding
	for _, values := range combos {
		out = append(out, bindingsFrom(SubstituteConfig(p.Config, values), path)...)
		out = append(out, ConditionBindings(SubstituteDisplay(p.Display, values))...)
	}
	return out
}

// DefaultValues returns the option each resolved variable starts on, which is
// what explicit validation checks a templated config against.
func DefaultValues(variables []models.DashboardVariable) VariableValues {
	out := VariableValues{}
	for _, v := range variables {
		if option, ok := v.DefaultOption(); ok {
			out[v.Name] = option
		}
	}
	return out
}
