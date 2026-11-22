//go:build freebsd || openbsd || netbsd || dragonfly

package unixauth

// defaultPasswdFile is /etc/master.passwd on the BSDs, the root-only file that
// holds the password hash; /etc/passwd there carries no hash and /etc/shadow
// does not exist.
const defaultPasswdFile = "/etc/master.passwd"
