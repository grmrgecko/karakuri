package sqlstore

import (
	"io"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs"
)

// memoryVFS gives SQLite private memory files, never host filesystem paths.
type memoryVFS struct {
	name string
	main *memoryFile
}

// memoryBudget bounds all Go-backed SQLite files owned by one connection.
type memoryBudget struct{ allocated int64 }

// memoryFile is exclusively owned by one disposable SQLite connection.
type memoryFile struct {
	data   []byte
	budget *memoryBudget
	retain bool
}

func (v *memoryVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	if name == v.name && flags&vfs.OPEN_MAIN_DB != 0 {
		return v.main, flags | vfs.OPEN_MEMORY, nil
	}
	// SQLite may spill a statement journal despite temp_store=MEMORY.
	// It gets memory under the same budget, with no path or shared database.
	if name == "" && flags&vfs.OPEN_TEMP_JOURNAL != 0 {
		return &memoryFile{budget: v.main.budget}, flags | vfs.OPEN_MEMORY, nil
	}
	return nil, flags, sqlite3.CANTOPEN
}

func (*memoryVFS) Delete(string, bool) error                   { return sqlite3.IOERR_DELETE }
func (*memoryVFS) Access(string, vfs.AccessFlag) (bool, error) { return false, nil }
func (v *memoryVFS) FullPathname(name string) (string, error) {
	if name != v.name {
		return "", sqlite3.CANTOPEN
	}
	return name, nil
}

// grow checks logical size and backing capacity before allocating.
func (f *memoryFile) grow(size int64) error {
	if size < 0 || size > maxDatabaseBytes {
		return sqlite3.FULL
	}
	if size <= int64(len(f.data)) {
		return nil
	}
	if size > int64(cap(f.data)) {
		capacity := max(size, int64(cap(f.data))*2, 4096)
		capacity = min(capacity, maxDatabaseBytes)
		additional := capacity - int64(cap(f.data))
		if f.budget.allocated+additional > maxDatabaseBytes {
			return sqlite3.FULL
		}
		data := make([]byte, len(f.data), int(capacity))
		copy(data, f.data)
		f.data = data
		f.budget.allocated += additional
	}
	old := len(f.data)
	f.data = f.data[:int(size)]
	clear(f.data[old:])
	return nil
}

func (f *memoryFile) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, sqlite3.IOERR_READ
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(b, f.data[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memoryFile) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || off > maxDatabaseBytes-int64(len(b)) {
		return 0, sqlite3.FULL
	}
	if err := f.grow(off + int64(len(b))); err != nil {
		return 0, err
	}
	return copy(f.data[off:], b), nil
}

func (f *memoryFile) Truncate(size int64) error {
	if err := f.grow(size); err != nil {
		return err
	}
	f.data = f.data[:int(size)]
	return nil
}

func (f *memoryFile) Size() (int64, error) { return int64(len(f.data)), nil }
func (f *memoryFile) Close() error {
	if !f.retain {
		f.budget.allocated -= int64(cap(f.data))
		f.data = nil
	}
	return nil
}
func (*memoryFile) Sync(vfs.SyncFlag) error          { return nil }
func (*memoryFile) Lock(vfs.LockLevel) error         { return nil }
func (*memoryFile) Unlock(vfs.LockLevel) error       { return nil }
func (*memoryFile) CheckReservedLock() (bool, error) { return false, nil }
func (*memoryFile) SectorSize() int                  { return 4096 }
func (*memoryFile) DeviceCharacteristics() vfs.DeviceCharacteristic {
	return vfs.IOCAP_ATOMIC | vfs.IOCAP_POWERSAFE_OVERWRITE | vfs.IOCAP_SUBPAGE_READ
}
