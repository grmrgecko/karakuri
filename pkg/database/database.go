package database

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	// The SQLite driver is SQLite compiled to WebAssembly, run in-process under
	// wazero — the same runtime the Python sandbox uses. No cgo, so the server
	// cross-compiles like the agent does. memdb registers the shared in-memory
	// VFS the tests open their databases through.
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "github.com/ncruces/go-sqlite3/vfs/memdb"
)

// DB is the package-level database connection used by all models and services.
var DB *gorm.DB

// Options holds the settings needed to open a database connection.
type Options struct {
	Type            string
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime int
	LogLevel        string
}

// sqliteDefaults are the connection pragmas a file-backed SQLite database
// needs to survive concurrent writers. The engine writes run history and
// variables from independent goroutines, and SQLite's default busy timeout is
// zero: without these, the second writer fails immediately with "database is
// locked" and its write is lost rather than retried.
//
// WAL lets readers run alongside a writer and the busy timeout makes writers
// queue instead of failing. _txlock=immediate is what makes the timeout apply
// to transactions: a deferred transaction that reads first and writes later
// has to upgrade its lock, and SQLite refuses that upgrade immediately rather
// than waiting, so a transaction like the run-history replace would fail even
// with a generous timeout set.
const sqliteDefaults = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"

// sqliteDSN adds the concurrency pragmas to a SQLite DSN, leaving any the
// caller already set alone. In-memory databases keep their DSN untouched: WAL
// does not apply to them and the tests open theirs through the memdb VFS.
// Anything carrying a query is returned as a file: URI, because the driver
// reads parameters only off a URI — appended to a bare path they would name a
// literal file called "karakuri.db?_pragma=...".
func sqliteDSN(dsn string) string {
	if strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory") || strings.Contains(dsn, "vfs=memdb") {
		return dsn
	}
	if !strings.Contains(dsn, "_pragma=") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + sqliteDefaults
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + filepath.ToSlash(dsn)
	}
	return dsn
}

// Init opens a GORM connection using the provided options and makes it DB.
// Closes any existing connection before opening a new one.
func Init(opts Options) error {
	if DB != nil {
		if sqlDB, err := DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	db, err := Open(opts)
	if err != nil {
		return err
	}
	DB = db
	return nil
}

// Open opens a GORM connection without touching DB, for callers that need a
// second database beside the server's own, such as a restore loading into a
// new file.
func Open(opts Options) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch opts.Type {
	case "sqlite":
		dialector = gormlite.Open(sqliteDSN(opts.DSN))
	case "postgres":
		dialector = postgres.Open(opts.DSN)
	case "mysql":
		dialector = mysql.Open(opts.DSN)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", opts.Type)
	}

	logLevel := logger.Silent
	if opts.LogLevel == "debug" {
		logLevel = logger.Info
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(opts.ConnMaxLifetime) * time.Second)
	return db, nil
}
