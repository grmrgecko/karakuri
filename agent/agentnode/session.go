package agentnode

import (
	"context"
	"path/filepath"

	"github.com/grmrgecko/karakuri/automation"
)

// HelperCommand is the argv the agent re-executes itself with to serve as a
// session helper. It is a hidden command rather than a separate binary so an
// install is still one file, and naming it here rather than in each node group
// is what keeps the two sides of the delegation in step.
var HelperCommand = []string{"session-helper"}

// SecureInputCommand is the argv the agent re-executes itself with on Windows
// to type into a locked console: a one-shot helper started as SYSTEM inside
// the target session, which is the only identity whose input the secure
// desktop accepts. Same hidden-command shape, same reason.
var SecureInputCommand = []string{"session-input"}

// NewSession builds the one automation session every desktop, GUI, and system
// node in this agent shares. It is one session rather than one per group
// because each of them is a real resource — an X connection, a portal session,
// a clipboard selection, and on a privileged agent a helper process — and two
// groups asking for the same desktop should not open two of each.
//
// Building it neither touches the display nor prompts, so a failure here rides
// onto each node and surfaces on first use rather than emptying the palette on
// a host with no display.
func NewSession(opts Options) (*automation.Session, error) {
	auto := automation.Options{
		AppID:              "karakuri",
		HelperCommand:      HelperCommand,
		SecureInputCommand: SecureInputCommand,
	}
	// The portal's restore token lives with the rest of the agent's state, so
	// consent granted once is not asked for again on the next start. A
	// privileged agent overrides this inside the helper with a persistent path
	// in the delegated user's config directory.
	if opts.Dir != "" {
		auto.TokenStore = automation.FileTokenStore(filepath.Join(opts.Dir, "automation"))
	}
	if opts.Logf != nil {
		auto.Logf = opts.Logf
	}
	return automation.New(context.Background(), auto)
}
