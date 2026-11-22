package models

import (
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserAndGroupSearchIsLiteral locks that the share dialog's search finds
// only names that hold the query as written. The query was used as a LIKE
// pattern, so searching home_admin also returned home-admin, and a percent
// sign listed every user and group.
func TestUserAndGroupSearchIsLiteral(t *testing.T) {
	testDB(t)
	for _, name := range []string{"home_admin", "home-admin", "guest"} {
		require.NoError(t, db.DB.Create(&User{Username: name, Email: name + "@example.invalid", PasswordHash: "x"}).Error)
		require.NoError(t, db.DB.Create(&UserGroup{Name: name}).Error)
	}
	users := func(q string) []string {
		rows, err := SearchUsers(q, 20)
		require.NoError(t, err)
		out := []string{}
		for _, u := range rows {
			out = append(out, u.Username)
		}
		return out
	}
	groups := func(q string) []string {
		rows, err := SearchUserGroups(q, 20)
		require.NoError(t, err)
		out := []string{}
		for _, g := range rows {
			out = append(out, g.Name)
		}
		return out
	}

	tests := []struct {
		query string
		want  []string
		why   string
	}{
		{"home", []string{"home-admin", "home_admin"}, "a substring finds every name that holds it"},
		{"home_admin", []string{"home_admin"}, "an underscore matches only an underscore"},
		{"%", []string{}, "a percent sign matches only a percent sign"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, users(tc.query), "users for %q: %s", tc.query, tc.why)
		assert.Equal(t, tc.want, groups(tc.query), "groups for %q: %s", tc.query, tc.why)
	}
}
