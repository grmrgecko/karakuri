package models

import (
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDB opens an empty in-memory database holding every model's table.
func testDB(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(All()...))
}

// TestListedDashboardsMatchAccess locks that the dashboard list holds every
// dashboard the access check lets the user open. A dashboard group's owner
// holds admin on whatever sits in the group, but the list looked only at
// shares on the group, so a dashboard a collaborator added to someone's
// group opened by its address and never appeared in that owner's list.
func TestListedDashboardsMatchAccess(t *testing.T) {
	testDB(t)
	groupOwner := &User{Username: "owner", Email: "owner@example.test", PasswordHash: "x"}
	author := &User{Username: "author", Email: "author@example.test", PasswordHash: "x"}
	stranger := &User{Username: "stranger", Email: "stranger@example.test", PasswordHash: "x"}
	for _, u := range []*User{groupOwner, author, stranger} {
		require.NoError(t, db.DB.Create(u).Error)
	}
	group := &DashboardGroup{Name: "House", OwnerID: groupOwner.ID}
	require.NoError(t, db.DB.Create(group).Error)
	grouped := &Dashboard{Name: "Kitchen", OwnerID: author.ID, GroupID: &group.ID}
	loose := &Dashboard{Name: "Private", OwnerID: author.ID}
	require.NoError(t, db.DB.Create(grouped).Error)
	require.NoError(t, db.DB.Create(loose).Error)

	listed := func(user uuid.UUID) []string {
		rows, total, err := ListAccessibleDashboards(user, nil, 1, 50)
		require.NoError(t, err)
		names := make([]string, 0, len(rows))
		for _, d := range rows {
			names = append(names, d.Name)
		}
		assert.Len(t, names, int(total), "the count must describe the rows listed")
		return names
	}

	ok, err := CanAccessDashboard(grouped.ID, groupOwner.ID, nil, DashboardAccessAdmin)
	require.NoError(t, err)
	require.True(t, ok, "a group's owner holds admin on a dashboard in the group")
	assert.Equal(t, []string{"Kitchen"}, listed(groupOwner.ID), "the group's owner is listed the dashboard they may open, and not the author's ungrouped one")
	assert.Equal(t, []string{"Kitchen", "Private"}, listed(author.ID), "an owner is listed their own dashboards")
	assert.Empty(t, listed(stranger.ID), "a user with no grant is listed nothing")
}
