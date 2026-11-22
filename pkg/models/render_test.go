package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupersededScheduleRendersAreOlderOnes locks that a finished scheduled
// render expires only the renders before it. Every other job of the schedule
// was expired, so when a render outlasted its schedule's interval the next
// one, already queued or running, was swept with its file as soon as it
// landed and the schedule's URL went back to the older picture.
func TestSupersededScheduleRendersAreOlderOnes(t *testing.T) {
	testDB(t)
	owner := &User{Username: "owner", Email: "owner@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)
	dashboard := &Dashboard{Name: "Frame", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(dashboard).Error)
	schedule := &RenderSchedule{DashboardID: dashboard.ID, CronExpr: "* * * * *"}
	require.NoError(t, CreateRenderSchedule(schedule))

	start := time.Now().Add(-10 * time.Minute)
	expires := time.Now().Add(time.Hour)
	job := func(status string, created time.Time) uuid.UUID {
		j := &RenderJob{DashboardID: dashboard.ID, TokenHash: uuid.NewString(), Status: status,
			ScheduleID: &schedule.ID, ExpiresAt: expires, CreatedAt: created}
		require.NoError(t, CreateRenderJob(j))
		return j.ID
	}
	older := job(RenderStatusCompleted, start)
	finished := job(RenderStatusCompleted, start.Add(time.Minute))
	queued := job(RenderStatusPending, start.Add(2*time.Minute))

	require.NoError(t, ExpireSupersededScheduleRenders(schedule.ID, finished))

	expired := func(id uuid.UUID) bool {
		j, err := FindRenderJob(id)
		require.NoError(t, err)
		return j.ExpiresAt.Before(time.Now().Add(time.Second))
	}
	assert.True(t, expired(older), "the render before the one that landed is due for sweeping")
	assert.False(t, expired(finished), "the render that landed keeps its expiry")
	assert.False(t, expired(queued), "a render queued after the one that landed is newer and must not be swept")
}
