//go:build linux || freebsd || openbsd || netbsd || dragonfly

// Package unixauth authenticates Unix account passwords without cgo. Linux
// session actions use the host PAM stack through purego so modules that need
// the original credential participate. The hash verifier in this file remains
// available for Unix systems and callers that explicitly need local password
// file verification.
//
// Verification is done by grmrgecko/go-passwd, a libxcrypt-compatible pure-Go
// crypt library, so the schemes a shadow or master.passwd entry can carry are
// covered from one call: yescrypt ($y$, the Arch and Debian default),
// sha512crypt ($6$, the Rocky and FreeBSD default), sha256crypt ($5$), md5crypt
// ($1$), and bcrypt ($2*$, the OpenBSD default), among others.
package unixauth

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	passwd "github.com/grmrgecko/go-passwd"
)

// ErrNoPasswordLogin reports that the account cannot authenticate with a
// password at all: the hash field is locked (a leading "!" or "*") or empty. It
// is distinct from a wrong password so the caller can say why the unlock cannot
// proceed rather than reporting a bad credential.
var ErrNoPasswordLogin = errors.New("unixauth: account has no password login")

// Verify reports whether password produces hash, the crypt(3) string from a
// shadow or master.passwd entry. A false with a nil error is a wrong password;
// ErrNoPasswordLogin distinguishes an account that cannot be verified this way
// at all, and a scheme the crypt library cannot parse surfaces as an error
// naming it, so the caller routes it to the PAM helper rather than reading it
// as a wrong password.
func Verify(password, hash string) (bool, error) {
	hash = strings.TrimSpace(hash)
	switch {
	case hash == "" || hash == "!" || hash == "!!" || hash == "*" || hash == "x":
		return false, ErrNoPasswordLogin
	case strings.HasPrefix(hash, "!"):
		// A locked account keeps its old hash behind the "!" so it can be
		// unlocked later; while locked it authenticates nobody.
		return false, ErrNoPasswordLogin
	}

	// go-passwd logs the stored hash and the supplied one through the standard
	// logger on a mismatch, which would put a shadow hash in the agent's log on
	// every wrong password. Mute the default logger for the length of the call;
	// the boolean result is the whole answer we need.
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(io.Discard)
	ok, err := passwd.SCheckPassword(hash, password)
	log.SetOutput(prevOut)
	log.SetFlags(prevFlags)
	if err != nil {
		return false, fmt.Errorf("verifying a %s hash: %w", scheme(hash), err)
	}
	return ok, nil
}

// scheme names the hash's crypt id for a message, without echoing the hash.
func scheme(hash string) string {
	if !strings.HasPrefix(hash, "$") {
		return "descrypt"
	}
	if i := strings.IndexByte(hash[1:], '$'); i >= 0 {
		return "$" + hash[1:1+i] + "$"
	}
	return "unknown"
}
