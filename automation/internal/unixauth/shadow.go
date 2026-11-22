//go:build linux || freebsd || openbsd || netbsd || dragonfly

package unixauth

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrUserNotFound reports that the password database has no entry for the
// account, which on a host using local files means the user does not exist
// there. Darwin is excluded from this package: it authenticates through
// OpenDirectory, not a colon-delimited file, and its lock screen is reached
// through the authorization plugin rather than a local hash check.
var ErrUserNotFound = errors.New("unixauth: no password entry for user")

// passwdFile is the file the hash is read from, set per platform: /etc/shadow on
// Linux, /etc/master.passwd on the BSDs. It is a variable so a test can point it
// at a fixture without root.
var passwdFile = defaultPasswdFile

// HashForUser returns the password hash field from the user's line in the
// platform's password file. Reading it needs root, which the credentialed
// unlock already requires; a permission error surfaces as-is so the caller can
// say the agent is not privileged rather than that the user is unknown. The
// hash is the second colon-field on both /etc/shadow and /etc/master.passwd, so
// one parser serves both.
func HashForUser(user string) (string, error) {
	f, err := os.Open(passwdFile)
	if err != nil {
		return "", fmt.Errorf("reading the password database: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok || name != user {
			continue
		}
		hash, _, _ := strings.Cut(rest, ":")
		return hash, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning the password database: %w", err)
	}
	return "", fmt.Errorf("%w: %s", ErrUserNotFound, user)
}

// VerifyUser reads the user's stored hash and checks the password against it.
// It is the whole local-file credential check: a true means the password
// authenticates the account, and the errors from Verify and HashForUser tell a
// locked or unknown account apart from a wrong password.
func VerifyUser(user, password string) (bool, error) {
	hash, err := HashForUser(user)
	if err != nil {
		return false, err
	}
	return Verify(password, hash)
}
