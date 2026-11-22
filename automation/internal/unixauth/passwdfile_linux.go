//go:build linux

package unixauth

// defaultPasswdFile is /etc/shadow on Linux, where the password hash lives.
const defaultPasswdFile = "/etc/shadow"
