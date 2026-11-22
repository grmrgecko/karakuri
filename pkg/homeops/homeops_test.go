package homeops

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlowFailuresGroupByErrorAndRecovery locks how flow failures become
// attention items (045 FR-005, user story 1 scenarios 4 and 5):
//   - Failures group per flow and distinct error. Flow A failed 3 times with
//     "timeout" and once with "boom", giving 2 items with counts 3 and 1.
//   - A flow whose newest finished run succeeded is a recovered warning; one
//     whose newest finished run failed stays an error.
//   - A succeeded run with handled failures is not a failure, and a failure
//     older than 24 hours is not counted.
//   - A flow the caller holds nothing on is left out entirely.
func TestFlowFailuresGroupByErrorAndRecovery(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	user := newUser(t, "me")
	other := newUser(t, "other")
	failing := newFlow(t, "Porch lights", user)
	recovered := newFlow(t, "Heater", user)
	private := newFlow(t, "Someone else's", other)

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	newestTimeout := addRun(t, failing, "failed", "timeout", false, ago(10))
	addRun(t, failing, "failed", "timeout", false, ago(20))
	addRun(t, failing, "failed", "timeout", false, ago(30))
	addRun(t, failing, "failed", "boom", false, ago(40))
	addRun(t, failing, "succeeded", "", true, ago(5))
	addRun(t, failing, "failed", "timeout", false, now.Add(-25*time.Hour))
	addRun(t, recovered, "failed", "boom", false, ago(60))
	addRun(t, recovered, "succeeded", "", false, ago(15))
	addRun(t, private, "failed", "boom", false, ago(10))

	var res AttentionResult
	require.NoError(t, flowItems(context.Background(), user, &res, now))

	type got struct {
		flow, message, severity string
		count                   int
		recovered               bool
	}
	var items []got
	for _, it := range res.Items {
		items = append(items, got{it.SubjectName, it.Message, it.Severity, it.Count, it.Recovered})
	}
	// The handled-failure success at -5m is newer than the failures, but it
	// is a success, so it marks the failing flow recovered as well.
	assert.ElementsMatch(t, []got{
		{"Porch lights", "timeout", SeverityWarning, 3, true},
		{"Porch lights", "boom", SeverityWarning, 1, true},
		{"Heater", "boom", SeverityWarning, 1, true},
	}, items, "failures group by flow and error, skip other users' flows and old runs")
	for _, it := range res.Items {
		if it.Message == "timeout" {
			assert.Equal(t, newestTimeout.String(), it.RunID, "a group links to its newest failed run")
		}
	}
	require.NotNil(t, res.Summary.FlowFailures)
	assert.Equal(t, 5, *res.Summary.FlowFailures, "3 timeouts, 1 boom on Porch lights, and 1 boom on Heater are the readable failures in 24 hours")

	addRun(t, failing, "failed", "timeout", false, ago(1))
	res = AttentionResult{}
	require.NoError(t, flowItems(context.Background(), user, &res, now))
	for _, it := range res.Items {
		if it.SubjectName == "Porch lights" {
			assert.Equal(t, SeverityError, it.Severity, "a flow whose newest finished run failed is an error")
			assert.False(t, it.Recovered)
		}
	}
}

// newUser stores a user with no roles.
func newUser(t *testing.T, name string) uuid.UUID {
	t.Helper()
	u := &models.User{ID: uuid.New(), Username: name, Email: name + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(u).Error)
	return u.ID
}

// newFlow stores a flow owned by owner.
func newFlow(t *testing.T, name string, owner uuid.UUID) *models.Flow {
	t.Helper()
	f := &models.Flow{Name: name, Slug: models.Slugify(name), OwnerID: owner}
	require.NoError(t, db.DB.Create(f).Error)
	return f
}

// addRun stores a finished run of f.
func addRun(t *testing.T, f *models.Flow, status, errText string, hadFailures bool, at time.Time) uuid.UUID {
	t.Helper()
	ended := at.Add(time.Second)
	run := &models.Run{ID: uuid.New(), FlowID: f.ID, Status: status, Error: errText, HadFailures: hadFailures, StartedAt: at, EndedAt: &ended}
	require.NoError(t, db.DB.Create(run).Error)
	return run.ID
}
