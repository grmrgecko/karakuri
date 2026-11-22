// Package sqlstore executes authored SQLite against private memory snapshots.
package sqlstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	maxDatabaseBytes int64 = 16 << 20
	maxResultBytes         = 1 << 20
	maxSQLBytes            = 64 << 10
)

// Store keeps SQL snapshots below the installation's data directory.
type Store struct{ DataDir string }

// databaseLock serializes requests to one snapshot and counts pending users.
type databaseLock struct {
	token chan struct{}
	users int
}

var (
	activeQueries = make(chan struct{}, 4)
	lockMu        sync.Mutex
	locks         = make(map[string]*databaseLock)
)

// lockDatabase returns a cancellable lease without retaining unused identities.
func lockDatabase(ctx context.Context, key string) (func(), error) {
	lockMu.Lock()
	l := locks[key]
	if l == nil {
		l = &databaseLock{token: make(chan struct{}, 1)}
		locks[key] = l
	}
	l.users++
	lockMu.Unlock()
	releaseUser := func() {
		lockMu.Lock()
		defer lockMu.Unlock()
		l.users--
		if l.users == 0 {
			delete(locks, key)
		}
	}
	select {
	case l.token <- struct{}{}:
		return func() { <-l.token; releaseUser() }, nil
	case <-ctx.Done():
		releaseUser()
		return nil, ctx.Err()
	}
}

// Query executes one statement and persists only a completed successful write.
func (s Store) Query(ctx context.Context, flowID uuid.UUID, scope, sql string, params any, maxRows int) (*Result, error) {
	if scope == "" {
		scope = "flow"
	}
	name := "global.db"
	switch scope {
	case "flow":
		if flowID == uuid.Nil {
			return nil, fmt.Errorf("flow scope requires an executing flow identity")
		}
		name = "flow-" + flowID.String() + ".db"
	case "global":
	default:
		return nil, fmt.Errorf("sql scope must be flow or global")
	}
	if len(sql) == 0 || len(sql) > maxSQLBytes {
		return nil, fmt.Errorf("sql must contain 1–65536 bytes")
	}
	if strings.IndexByte(sql, 0) >= 0 {
		return nil, fmt.Errorf("sql must not contain NUL bytes")
	}
	if maxRows < 1 || maxRows > 10000 {
		return nil, fmt.Errorf("sql row limit must be between 1 and 10000")
	}
	path, err := filepath.Abs(s.DataDir)
	if err != nil {
		return nil, err
	}
	release, err := lockDatabase(ctx, filepath.Join(path, "sql", name))
	if err != nil {
		return nil, err
	}
	defer release()
	select {
	case activeQueries <- struct{}{}:
		defer func() { <-activeQueries }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := openRoot(path)
	if err != nil {
		return nil, fmt.Errorf("opening sql storage: %w", err)
	}
	defer root.Close()
	data, err := readSnapshot(root, name)
	if err != nil {
		return nil, fmt.Errorf("reading sql database: %w", err)
	}
	result, snapshot, changed, err := execute(ctx, data, sql, params, maxRows)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if changed {
		if err := saveSnapshot(ctx, root, name, snapshot); err != nil {
			return nil, fmt.Errorf("saving sql database: %w", err)
		}
	}
	return result, nil
}

// openRoot rejects substitution of the private directory with a symlink.
func openRoot(path string) (*os.Root, error) {
	parent, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := parent.Mkdir("sql", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := parent.Lstat("sql")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("sql storage must be a directory, not a symlink")
	}
	root, err := parent.OpenRoot("sql")
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("sql storage directory changed while opening")
	}
	return root, nil
}

// readSnapshot bounds allocation and refuses nonregular or replaced files.
func readSnapshot(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("sql snapshot must be a regular file, not a symlink")
	}
	if info.Size() > maxDatabaseBytes {
		return nil, fmt.Errorf("sql database exceeds 16 MiB")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("sql snapshot changed while opening")
	}
	data := make([]byte, int(info.Size()))
	_, err = io.ReadFull(f, data)
	if err != nil {
		return nil, err
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("sql snapshot changed while reading")
	}
	return data, nil
}

// saveSnapshot writes privately and replaces the scope file after synchronization.
func saveSnapshot(ctx context.Context, root *os.Root, name string, data []byte) error {
	temp := ".query-" + rand.Text()
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
