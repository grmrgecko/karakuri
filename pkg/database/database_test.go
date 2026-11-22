package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSQLiteDSN covers the concurrency pragmas appended to a SQLite DSN. The
// engine writes run history from several goroutines while the API reads it, so
// WAL and a busy timeout are what keep a run from failing on "database is
// locked". The rules that matter are which DSNs get them: a file-backed
// database does, an in-memory one cannot use WAL at all, and a DSN where the
// operator already set a pragma is theirs to own.
func TestSQLiteDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
		why  string
	}{
		{
			name: "bare path gains the pragmas as a file URI",
			dsn:  "/var/lib/karakuri/karakuri.db",
			want: "file:/var/lib/karakuri/karakuri.db?" + sqliteDefaults,
			why:  "the driver reads a query only off a URI; appended to a bare path it names a literal file",
		},
		{
			name: "existing query keeps its parameters",
			dsn:  "file:karakuri.db?cache=shared",
			want: "file:karakuri.db?cache=shared&" + sqliteDefaults,
			why:  "appending must not drop what the operator already set",
		},
		{
			name: "in-memory is left untouched",
			dsn:  "file::memory:",
			want: "file::memory:",
			why:  "there is no file to journal, so WAL does not apply",
		},
		{
			name: "memdb is left untouched",
			dsn:  "file:/shared?vfs=memdb",
			want: "file:/shared?vfs=memdb",
			why:  "the tests' shared in-memory VFS cannot journal either",
		},
		{
			name: "caller-set pragma wins",
			dsn:  "karakuri.db?_pragma=busy_timeout(100)",
			want: "file:karakuri.db?_pragma=busy_timeout(100)",
			why:  "an operator tuning a pragma must not have it silently doubled, but still needs the URI form",
		},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, sqliteDSN(c.dsn), "sqliteDSN(%q): %s", c.dsn, c.why)
	}
}
