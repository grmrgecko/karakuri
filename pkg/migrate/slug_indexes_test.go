package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	db "github.com/grmrgecko/karakuri/pkg/database"
)

// TestRelaxSlugIndexesSkipsOtherEngines locks that the slug index fix-up only
// runs on SQLite. It reads pragma_index_list, which PostgreSQL and MySQL do
// not have, so running it there failed AutoMigrate and stopped startup. The
// handle points at a closed port, so any statement it sends fails.
func TestRelaxSlugIndexesSkipsOtherEngines(t *testing.T) {
	pg, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 user=karakuri dbname=karakuri sslmode=disable connect_timeout=1"}),
		&gorm.Config{DisableAutomaticPing: true})
	require.NoError(t, err, "opening a PostgreSQL handle does not connect")
	var statements []string
	require.NoError(t, pg.Callback().Row().Before("gorm:row").Register("test:capture", func(tx *gorm.DB) {
		statements = append(statements, tx.Statement.SQL.String())
	}))
	previous := db.DB
	db.DB = pg
	t.Cleanup(func() { db.DB = previous })

	assert.NoError(t, relaxSlugIndexes(), "PostgreSQL never had the unique slug indexes to relax")
	assert.Empty(t, statements, "PostgreSQL has no pragma_index_list")
}
