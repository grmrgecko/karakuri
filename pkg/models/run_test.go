package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestPruneRunHistoryBoundsOffsetOnMySQL locks that the query finding runs
// past the hundred a flow keeps carries a LIMIT. MySQL accepts OFFSET only
// after LIMIT, so the bare OFFSET was a syntax error there and every run
// start and finish, which prune in the same transaction, failed.
func TestPruneRunHistoryBoundsOffsetOnMySQL(t *testing.T) {
	dry, err := gorm.Open(mysql.New(mysql.Config{DSN: "karakuri@tcp(127.0.0.1:1)/karakuri", SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err, "a dry-run session builds statements without a server")
	var statements []string
	require.NoError(t, dry.Callback().Query().After("gorm:query").Register("test:capture", func(tx *gorm.DB) {
		statements = append(statements, tx.Statement.SQL.String())
	}))

	require.NoError(t, pruneRunHistory(dry, uuid.New()))
	require.Len(t, statements, 1, "a dry run reads no rows, so only the select is built")
	assert.Regexp(t, `LIMIT \? OFFSET \?$`, statements[0], "MySQL refuses an OFFSET that does not follow a LIMIT")
}
