//go:build linux

// Package nssuser resolves Linux users through libc NSS without cgo.
package nssuser

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	initialBufferSize = 4096
	maximumBufferSize = 1 << 20
	erange            = 34
)

type passwd struct {
	name   unsafe.Pointer
	passwd unsafe.Pointer
	uid    uint32
	gid    uint32
	gecos  unsafe.Pointer
	dir    unsafe.Pointer
	shell  unsafe.Pointer
}

// User contains the account fields needed to enter a desktop session.
type User struct {
	Name     string
	HomeDir  string
	GID      uint32
	GroupIDs []uint32
}

var libc struct {
	once sync.Once

	getpwuidr    func(uint32, *passwd, *byte, uintptr, **passwd) int32
	getgrouplist func(string, uint32, *uint32, *int32) int32
	strlen       func(unsafe.Pointer) uintptr
}

// LookupID resolves an account through the host's configured NSS providers.
// Go's cgo-free os/user implementation reads /etc/passwd directly, which
// excludes systemd-homed, SSSD, and other users synthesized by libc NSS.
func LookupID(uid uint32) (User, error) {
	loadLibc()

	var entry passwd
	for size := initialBufferSize; size <= maximumBufferSize; size *= 2 {
		buffer := make([]byte, size)
		var result *passwd
		status := libc.getpwuidr(uid, &entry, &buffer[0], uintptr(len(buffer)), &result)
		switch {
		case status == erange:
			continue
		case status != 0:
			return User{}, fmt.Errorf("resolving uid %d through NSS: %w", uid, syscall.Errno(status))
		case result == nil:
			return User{}, fmt.Errorf("resolving uid %d through NSS: account not found", uid)
		}

		user := User{
			Name:    cString(entry.name),
			HomeDir: cString(entry.dir),
			GID:     entry.gid,
		}
		user.GroupIDs = groupIDs(user.Name, user.GID)
		return user, nil
	}
	return User{}, fmt.Errorf("resolving uid %d through NSS: account record exceeds %d bytes", uid, maximumBufferSize)
}

func loadLibc() {
	libc.once.Do(func() {
		purego.RegisterLibFunc(&libc.getpwuidr, purego.RTLD_DEFAULT, "getpwuid_r")
		purego.RegisterLibFunc(&libc.getgrouplist, purego.RTLD_DEFAULT, "getgrouplist")
		purego.RegisterLibFunc(&libc.strlen, purego.RTLD_DEFAULT, "strlen")
	})
}

func groupIDs(name string, primary uint32) []uint32 {
	groups := make([]uint32, 16)
	count := int32(len(groups))
	if libc.getgrouplist(name, primary, &groups[0], &count) == -1 {
		if count <= 0 || count > 65536 {
			return []uint32{primary}
		}
		groups = make([]uint32, count)
		if libc.getgrouplist(name, primary, &groups[0], &count) == -1 {
			return []uint32{primary}
		}
	}
	return groups[:count]
}

func cString(pointer unsafe.Pointer) string {
	if pointer == nil {
		return ""
	}
	return unsafe.String((*byte)(pointer), libc.strlen(pointer))
}
