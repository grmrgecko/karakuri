//go:build windows

package automation

import (
	"context"
	"fmt"
	"os"

	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
	win "github.com/grmrgecko/karakuri/automation/internal/windows"
)

// spawnHelper starts this binary again as the user signed into the console, on
// their visible desktop, and returns the framed pipe to it. A system service
// is outside every interactive session, and Windows delivers synthesized input
// only to a process inside the target one, so reaching the desktop means
// starting a process there — which only SYSTEM may do. The child runs as the
// console user, holding no more than the desktop it drives.
//
// The console session is resolved at spawn rather than carried from New, so a
// helper the client restarts after a user signs out or fast-switches picks up
// whoever is at the console now, the way the retry-on-peer-gone path intends.
// The target's role here is only to say delegation is wanted; which session it
// lands in is decided fresh each time.
func spawnHelper(ctx context.Context, opts Options, target DelegateTarget) (*hrpc.Conn, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("finding this binary to start the desktop helper: %w", err)
	}
	r, w, stop, err := win.SpawnInteractive(ctx, exe, opts.HelperCommand, opts.Logf)
	if err != nil {
		return nil, nil, err
	}
	return hrpc.NewConn(r, w), stop, nil
}
