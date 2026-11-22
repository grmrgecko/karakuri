package main

import (
	"context"

	"github.com/grmrgecko/karakuri/automation"
)

// SessionHelperCmd is the other side of the agent's own delegation, not a
// command an operator runs. A privileged agent cannot touch a desktop — the
// session bus answers its owner and nobody else, and a root process's
// synthesized keys are delivered to no session at all — so it starts this
// binary as the logged-in user and drives their session through it. The
// protocol rides standard input and output, except on macOS, where launchd
// starts the helper and gives it no pipes: there the socket path arrives in
// the environment and the helper serves on that instead. Either way this
// command prints nothing and takes no arguments.
type SessionHelperCmd struct{}

// Run serves the automation session until the parent closes the wire.
func (SessionHelperCmd) Run() error {
	return automation.ServeHelperCommand(context.Background(), automation.Options{
		AppID: "karakuri",
	})
}

// SessionInputCmd is the Windows counterpart of the session helper: a
// one-shot helper the privileged agent starts as SYSTEM inside the session
// whose lock is being dismissed, where it types the account's password into
// the secure desktop. Like SessionHelper it is not a command an operator
// runs — the request arrives on standard input, the password among it.
type SessionInputCmd struct{}

// Run reads one secure-input request, performs it, and writes one result
// line.
func (SessionInputCmd) Run() error {
	return automation.ServeSecureInputCommand(context.Background(), automation.Options{
		AppID: "karakuri",
	})
}
