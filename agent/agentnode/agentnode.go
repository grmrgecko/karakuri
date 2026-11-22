// Package agentnode holds what every one of the agent's node groups needs: the
// options resolved once at startup, the limits derived from them, and the
// helpers for reading a message, driving a session tool, and decoding what a
// program wrote. The node groups beside it (exec, storage, desktop, gui,
// system) import this and nothing of each other, so a helper used by two of
// them lives here rather than in whichever group happened to need it first.
package agentnode

import (
	"context"
	"time"
)

// Options are the limits and mappings the nodes read, resolved once from the
// command line and agent.yaml.
type Options struct {
	// ExecTimeout is the agent-wide default per-execution budget in seconds.
	ExecTimeout int
	// MaxOutput caps one file, clipboard, or log-line read, in bytes. Process
	// output is spooled to disk and returned whole.
	MaxOutput int
	// Dir is the agent's state directory, which is where the automation
	// session keeps the portal consent token it restores with.
	Dir string
	// Logf is where the automation session reports what it chose and what went
	// wrong inside a session helper; nil discards it.
	Logf func(format string, args ...any)
	// Credentials resolves a stored credential reference to the account and its
	// password, for the Session node's credentialed unlock and login. It reaches
	// the server for the store key, so it is nil until the agent is enrolled and
	// running; a node that needs it and finds it nil reports that credentialed
	// unlock is not configured.
	Credentials CredentialResolver
}

// CredentialResolver returns the account and password behind a stored credential
// reference. The password is held only long enough to authenticate with and is
// the caller's to forget.
type CredentialResolver func(ctx context.Context, ref string) (user, password string, err error)

// DefaultMaxOutput caps single reads when the agent was started without
// --max-output. 256KB is what one message can carry home comfortably in the
// gRPC stream.
const DefaultMaxOutput = 256 << 10

// defaultExecCeiling bounds a single execution when neither the node nor the
// agent config names a budget.
const defaultExecCeiling = 60 * time.Second

// ResolveTimeout resolves a per-execution budget: the node's value, the agent
// default, then the ceiling.
func ResolveTimeout(nodeSeconds, agentSeconds int) time.Duration {
	switch {
	case nodeSeconds > 0:
		return time.Duration(nodeSeconds) * time.Second
	case agentSeconds > 0:
		return time.Duration(agentSeconds) * time.Second
	}
	return defaultExecCeiling
}

// ReadLimit is the ceiling on a single read, which is the agent's output
// budget: whatever is read has to cross the wire home in one message.
func ReadLimit(maxOutput int) int {
	if maxOutput > 0 {
		return maxOutput
	}
	return DefaultMaxOutput
}
