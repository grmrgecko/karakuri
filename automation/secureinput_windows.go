//go:build windows

package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/internal/windows"
)

// ServeSecureInputCommand is what a caller's hidden secure-input subcommand
// runs: it reads one request from standard input, types it into the console
// this process was started in, and writes one result line back. The parent
// starts it as SYSTEM inside the session whose lock is being dismissed, which
// is the only way synthetic input reaches the secure desktop.
func ServeSecureInputCommand(ctx context.Context, opts Options) error {
	return windows.ServeSecureInput(ctx, opts.Logf)
}
