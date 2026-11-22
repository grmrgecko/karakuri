// Package version carries the build identity shared by the project binaries. The
// variables are stamped at link time by the Makefile from the VERSION file and
// the git checkout; the constant names the project itself. The server reports
// it from `karakuri version`, karakuri-auto prints it from its version command,
// and the agent sends it to the host at enrollment, on connect, and with every
// renewal. All three have to agree so an operator can match them to one build.
package version

// These variables are set at build time via -ldflags. The defaults apply to
// plain `go build` output, which carries no VERSION file or git metadata.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Name is the project's own name, for the places a binary prints it.
const Name = "karakuri"

// String renders the build identity as one line, appending the commit and build
// date when the binary was stamped by the Makefile.
func String() string {
	s := Version
	switch {
	case Commit != "" && Date != "":
		s += " (" + Commit + ", " + Date + ")"
	case Commit != "":
		s += " (" + Commit + ")"
	case Date != "":
		s += " (" + Date + ")"
	}
	return s
}
