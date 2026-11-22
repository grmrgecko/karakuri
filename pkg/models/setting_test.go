package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettingsPrefixIsLiteral locks that a settings prefix names only the
// keys that start with it. The prefix was used as a LIKE pattern as written,
// so the underscore in plugins.my_plugin. matched any character: reading that
// plugin's settings returned plugins.my-plugin.'s too, and removing it from
// disk deleted both.
func TestSettingsPrefixIsLiteral(t *testing.T) {
	testDB(t)
	for _, key := range []string{"plugins.my_plugin.token", "plugins.my-plugin.token", "plugins.other.token"} {
		require.NoError(t, SetSetting(key, "v"))
	}

	keys := func(prefix string) []string {
		rows, err := ListSettingsByPrefix(prefix)
		require.NoError(t, err)
		out := make([]string, 0, len(rows))
		for _, s := range rows {
			out = append(out, s.Key)
		}
		return out
	}

	assert.Equal(t, []string{"plugins.my_plugin.token"}, keys("plugins.my_plugin."),
		"an underscore in the prefix must match only an underscore")
	assert.Empty(t, keys("plugins.%."), "a percent sign in the prefix must match only a percent sign")

	require.NoError(t, DeleteSettingsByPrefix("plugins.my_plugin."))
	assert.Equal(t, []string{"plugins.my-plugin.token", "plugins.other.token"}, keys("plugins."),
		"deleting one plugin's settings must leave a plugin whose name differs at the underscore alone")
}
