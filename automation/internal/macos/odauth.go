//go:build darwin

package macos

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// Password verification goes through Directory Services' own C API rather than
// spawning `dscl`, because the password would ride on the command line and
// `ps` is world-readable. ODRecordVerifyPassword is the call the login window
// itself makes: opendirectoryd performs the verification, so an unprivileged
// process can check its own account's password and a root process can check
// anybody's, with a failed attempt counting the same way a failed login does.

// kODErrCredentialsInvalid is the OpenDirectory code for a password that did
// not match, reported by CFErrorGetCode on the error ODRecordVerifyPassword
// fills in.
const kODErrCredentialsInvalid = -14090

var (
	odNodeCreateWithName func(alloc, session, name uintptr, errOut *uintptr) uintptr
	odNodeCopyRecord     func(node, recordType, recordName, attrs uintptr, errOut *uintptr) uintptr
	odRecordVerifyPass   func(record, password uintptr, errOut *uintptr) bool
	cfErrorCopyDesc      func(errRef uintptr) uintptr
	cfErrorGetCode       func(errRef uintptr) int32

	odOnce sync.Once
	odErr  error
)

// loadODSymbols resolves the Directory Services calls once. They live in the
// CFOpenDirectory subframework, not the OpenDirectory umbrella, which exports
// only the Objective-C classes.
func loadODSymbols() error {
	odOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/OpenDirectory.framework/Versions/A/Frameworks/CFOpenDirectory.framework/CFOpenDirectory",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			odErr = fmt.Errorf("opening CFOpenDirectory: %w", err)
			return
		}
		defer func() {
			// A symbol that moved in an OS update lands here as an error
			// rather than a crash.
			if r := recover(); r != nil {
				odErr = fmt.Errorf("a Directory Services symbol did not resolve: %v", r)
			}
		}()
		purego.RegisterLibFunc(&odNodeCreateWithName, lib, "ODNodeCreateWithName")
		purego.RegisterLibFunc(&odNodeCopyRecord, lib, "ODNodeCopyRecord")
		purego.RegisterLibFunc(&odRecordVerifyPass, lib, "ODRecordVerifyPassword")

		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			odErr = fmt.Errorf("opening CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&cfErrorCopyDesc, cf, "CFErrorCopyDescription")
		purego.RegisterLibFunc(&cfErrorGetCode, cf, "CFErrorGetCode")
	})
	return odErr
}

// verifyPassword checks an account's password against the local Directory
// Services node and reports whether it was accepted. A mismatch returns a
// plain wrong-password error; anything the directory cannot answer (no such
// user, a network directory that refused the method) names what it said.
func verifyPassword(user, password string) error {
	if err := loadODSymbols(); err != nil {
		return err
	}

	userRef := cfStr(user)
	defer cfRelease(userRef)
	usersRef := cfStr("dsRecTypeStandard:Users")
	defer cfRelease(usersRef)
	nodeName := cfStr("/Local/Default")
	defer cfRelease(nodeName)
	passRef := cfStr(password)
	defer cfRelease(passRef)

	var errRef uintptr
	node := odNodeCreateWithName(0, 0, nodeName, &errRef)
	if node == 0 {
		return fmt.Errorf("opening the local Directory Services node: %s", cfErrorText(errRef))
	}
	defer cfRelease(node)

	record := odNodeCopyRecord(node, usersRef, userRef, 0, &errRef)
	if record == 0 {
		return fmt.Errorf("no Directory Services record for user %s: %s", user, cfErrorText(errRef))
	}
	defer cfRelease(record)

	errRef = 0
	if odRecordVerifyPass(record, passRef, &errRef) {
		return nil
	}
	if errRef != 0 && cfErrorGetCode(errRef) == kODErrCredentialsInvalid {
		return fmt.Errorf("wrong password for %s", user)
	}
	return fmt.Errorf("Directory Services would not verify the password for %s: %s", user, cfErrorText(errRef))
}

// cfErrorText reads a CFErrorRef's description as text, for error strings.
func cfErrorText(errRef uintptr) string {
	if errRef == 0 || cfErrorCopyDesc == nil {
		return "no detail"
	}
	desc := cfErrorCopyDesc(errRef)
	if desc == 0 {
		return "no detail"
	}
	defer cfRelease(desc)
	return cfStringGo(desc)
}
