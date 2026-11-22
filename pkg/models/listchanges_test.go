package models

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListChangesAnnounceEveryWritePath locks that a write to a table the flow
// or dashboard list draws from produces one notice on that list's topic,
// whatever made it, and that an unrelated write produces none. Pages open in
// other sessions depend on it to show flows and dashboards as they are added,
// moved, shared, and removed.
func TestListChangesAnnounceEveryWritePath(t *testing.T) {
	testDB(t)

	var mu sync.Mutex
	heard := map[string]int{}
	require.NoError(t, WatchListChanges(func(topic string) {
		mu.Lock()
		heard[topic]++
		mu.Unlock()
	}))
	settle := func() map[string]int {
		time.Sleep(listChangeDelay * 3)
		mu.Lock()
		defer mu.Unlock()
		out := heard
		heard = map[string]int{}
		return out
	}

	owner := &User{Username: "owner", Email: "owner@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)
	assert.Empty(t, settle(), "a table no list draws from announces nothing")

	flow := &Flow{ID: uuid.New(), Name: "Porch", Slug: "porch", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(flow).Error)
	folder := &Folder{Name: "Outside", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(folder).Error)
	require.NoError(t, db.DB.Model(flow).Update("folder_id", folder.ID).Error)
	assert.Equal(t, map[string]int{FlowsListTopic: 1}, settle(),
		"a create, a folder, and a move in one burst announce the flow list once")

	require.NoError(t, db.DB.Delete(flow).Error)
	assert.Equal(t, map[string]int{FlowsListTopic: 1}, settle(), "a delete announces the flow list")

	dash := &Dashboard{Name: "Kitchen", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(dash).Error)
	assert.Equal(t, map[string]int{DashboardsListTopic: 1}, settle(), "a dashboard announces the dashboard list")

	require.NoError(t, db.DB.Model(&Dashboard{}).Where("id = ?", uuid.New()).Update("name", "Nothing").Error)
	assert.Empty(t, settle(), "an update that matched no row announces nothing")
}
