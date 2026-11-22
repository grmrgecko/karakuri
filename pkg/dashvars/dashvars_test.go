package dashvars

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// haStates is shaped like Home Assistant's /api/states: person entities carry
// friendly_name, and an input_select lists its choices in the options
// attribute with the current one as its state.
var haStates = []homeassistant.Entity{
	{EntityID: "person.james", State: "unknown", Attributes: map[string]any{"friendly_name": "James"}},
	{EntityID: "person.mikinzi", State: "home", Attributes: map[string]any{"friendly_name": "Mikinzi"}},
	{EntityID: "person_tracker.phone", State: "home", Attributes: map[string]any{"friendly_name": "Phone"}},
	{EntityID: "input_select.active_person", State: "Mikinzi", Attributes: map[string]any{
		"friendly_name": "Active person", "options": []any{"James", "Mikinzi", "Guest"},
	}},
}

// TestQueryOptions locks how a query turns states into options, which are
// also what the layout grants. The entity pattern's dot is literal, so
// person_tracker.phone stays out of person.*. A value group extracts the part
// of the ID widgets template on, and the friendly name stays the label. A
// select's options follow the same regex, and its state maps to the option
// value it produced, which is what a followed variable takes; the regex drops
// Guest.
func TestQueryOptions(t *testing.T) {
	tests := []struct {
		name     string
		query    models.VariableQuery
		want     []models.VariableOption
		followed string
	}{
		{
			name:  "entity pattern",
			query: models.VariableQuery{Source: models.QueryHAEntities, Pattern: "person.*"},
			want:  []models.VariableOption{{Value: "person.james", Label: "James"}, {Value: "person.mikinzi", Label: "Mikinzi"}},
		},
		{
			name:  "entity pattern with a value group, sorted descending",
			query: models.VariableQuery{Source: models.QueryHAEntities, Pattern: "person.*", Regex: `^person\.(?<value>.+)$`, Sort: "desc"},
			want:  []models.VariableOption{{Value: "mikinzi", Label: "Mikinzi"}, {Value: "james", Label: "James"}},
		},
		{
			name:     "select options a regex keeps, followed",
			query:    models.VariableQuery{Source: models.QueryHASelect, EntityID: "input_select.active_person", Regex: `^(?<value>James|Mikinzi)$`, Follow: true},
			want:     []models.VariableOption{{Value: "James"}, {Value: "Mikinzi"}},
			followed: "Mikinzi",
		},
	}
	for _, tc := range tests {
		r := FromStates(&tc.query, haStates)
		require.NoError(t, r.Err, "query %s must resolve", tc.name)
		assert.Equal(t, tc.want, r.Options, "options for %s", tc.name)
		assert.Equal(t, tc.followed, r.Followed, "followed value for %s", tc.name)
	}

	missing := FromStates(&models.VariableQuery{Source: models.QueryHASelect, EntityID: "input_select.gone"}, haStates)
	assert.Error(t, missing.Err, "a select entity Home Assistant does not report must fail rather than resolve to an empty grant silently")
}
