package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateFaceplate pins the lenient and strict rules. Drafts may hold
// unfinished controls, but a stored value that does not fit its control is
// refused even in a draft, so Apply can never put a bad value into a version.
func TestValidateFaceplate(t *testing.T) {
	min, max := 0.0, 100.0
	number := FaceplateControl{ID: "max", Label: "Max", Type: ControlNumber, Min: &min, Max: &max}
	choice := FaceplateControl{ID: "gallery", Label: "Gallery", Type: ControlString,
		Options: []ControlOption{{Value: "classic"}, {Value: "modern"}}}
	for _, tc := range []struct {
		name    string
		cfg     FaceplateConfig
		lenient bool
		ok      bool
	}{
		{"a finished faceplate", FaceplateConfig{Name: "Simple", Controls: []FaceplateControl{number, choice}, Values: map[string]any{"max": 70.0, "gallery": "modern"}}, false, true},
		{"an unnamed tab in a draft", FaceplateConfig{Controls: []FaceplateControl{{ID: "x", Type: ControlBoolean}}}, true, true},
		{"an unnamed tab at deploy", FaceplateConfig{Controls: []FaceplateControl{number}}, false, false},
		{"a value above the maximum, even in a draft", FaceplateConfig{Controls: []FaceplateControl{number}, Values: map[string]any{"max": 150.0}}, true, false},
		{"text where a number belongs", FaceplateConfig{Name: "S", Controls: []FaceplateControl{number}, Values: map[string]any{"max": "high"}}, false, false},
		{"a value outside the options", FaceplateConfig{Name: "S", Controls: []FaceplateControl{choice}, Values: map[string]any{"gallery": "baroque"}}, false, false},
		{"a duplicated control id", FaceplateConfig{Name: "S", Controls: []FaceplateControl{number, number}}, false, false},
		{"an entity without a connection", FaceplateConfig{Name: "S", Controls: []FaceplateControl{{ID: "sensor", Label: "Sensor", Type: ControlEntity}}, Values: map[string]any{"sensor": map[string]any{"entity_id": "sensor.lux"}}}, false, false},
	} {
		problems := ValidateFaceplate(tc.cfg, tc.lenient)
		assert.Equalf(t, tc.ok, len(problems) == 0, "%s: %v", tc.name, problems)
	}
}

// TestControlValueFallsBackToDefault locks that a control without an applied
// value reads its default. A stored null passes validation as "no value", so
// it has to read the same way: it was returned as the value, and a number
// control with a default of 20 fed null into the setting wired to it.
func TestControlValueFallsBackToDefault(t *testing.T) {
	control := FaceplateControl{ID: "level", Label: "Level", Type: ControlNumber, Default: 20.0}
	for _, tc := range []struct {
		name   string
		values map[string]any
		want   any
	}{
		{"no stored value", nil, 20.0},
		{"a stored null", map[string]any{"level": nil}, 20.0},
		{"an applied value", map[string]any{"level": 35.0}, 35.0},
		{"an applied zero", map[string]any{"level": 0.0}, 0.0},
	} {
		cfg := FaceplateConfig{Controls: []FaceplateControl{control}, Values: tc.values}
		assert.Emptyf(t, ValidateFaceplate(cfg, true), "%s: the config must be one validation accepts", tc.name)
		assert.Equalf(t, tc.want, cfg.ControlValue(control), "%s: the control must read its applied value, or its default when none is applied", tc.name)
	}
}
