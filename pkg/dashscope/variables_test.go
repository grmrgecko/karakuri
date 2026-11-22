package dashscope

import (
	"fmt"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
)

// TestVariableBindingsGrantOnlyOptions locks the grant a templated placement
// produces. A binding to sensor.${person}_steps must grant the entity each
// option names and nothing else, because the options are the author's list of
// what viewers may read; a viewer choosing a value outside them must find
// nothing granted. A placement over MaxVariableCombinations grants none of its
// bindings rather than an unbounded walk: two variables of 20 options each are
// 20 x 20 = 400 combinations, past the 256 limit.
func TestVariableBindingsGrantOnlyOptions(t *testing.T) {
	person := models.DashboardVariable{Name: "person", Options: []models.VariableOption{
		{Value: "james", Label: "James"}, {Value: "wife", Label: "Sam"},
	}}
	options := func(n int) []models.VariableOption {
		out := make([]models.VariableOption, n)
		for i := range out {
			out[i] = models.VariableOption{Value: fmt.Sprintf("v%d", i)}
		}
		return out
	}
	wide := []models.DashboardVariable{{Name: "a", Options: options(20)}, {Name: "b", Options: options(20)}}

	tests := []struct {
		name      string
		config    string
		display   *models.WidgetDisplay
		variables []models.DashboardVariable
		want      []string
	}{
		{
			name:      "a templated entity",
			config:    `{"source":"ha","displayOnly":true,"connectionId":"c1","entityId":"sensor.${person}_steps","name":"${person:text}"}`,
			variables: []models.DashboardVariable{person},
			want:      []string{"sensor.james_steps", "sensor.wife_steps"},
		},
		{
			name:   "a templated condition source",
			config: `{}`,
			display: &models.WidgetDisplay{Conditions: []models.Condition{{
				Kind: models.ConditionState, Source: "ha", ConnectionID: "c1", EntityID: "person.${person}", Operator: "equals", Value: "home",
			}}},
			variables: []models.DashboardVariable{person},
			want:      []string{"person.james", "person.wife"},
		},
		{
			name:   "a templated status cue source",
			config: `{}`,
			display: &models.WidgetDisplay{Cues: []models.StatusCue{{Conditions: []models.Condition{{
				Kind: models.ConditionState, Source: "ha", ConnectionID: "c1", EntityID: "cover.${person}_garage", Operator: "equals", Value: "open",
			}}}}},
			variables: []models.DashboardVariable{person},
			want:      []string{"cover.james_garage", "cover.wife_garage"},
		},
		{
			name:   "a reference to a variable the dashboard does not define",
			config: `{"source":"ha","displayOnly":true,"connectionId":"c1","entityId":"sensor.${room}"}`,
			want:   []string{"sensor.${room}"},
		},
		{
			name:      "a placement over the combination limit",
			config:    `{"source":"ha","displayOnly":true,"connectionId":"c1","entityId":"sensor.${a}_${b}"}`,
			variables: wide,
			want:      nil,
		},
	}
	for _, tc := range tests {
		p := models.DashboardWidget{Config: tc.config, Display: tc.display}
		var got []string
		for _, b := range placementBindings(p, ".", tc.variables) {
			if b.EntityID != "" {
				got = append(got, b.EntityID)
			}
		}
		assert.ElementsMatch(t, tc.want, got, "granted entities for %s", tc.name)
	}
}
