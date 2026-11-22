//go:build !unix && !windows

package automation

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// spawnHelper is unreachable on a platform that never picks a delegate target,
// and exists so the one delegation path compiles everywhere rather than being
// spread across build tags of its own.
func spawnHelper(ctx context.Context, opts Options, target DelegateTarget) (*hrpc.Conn, func(), error) {
	return nil, nil, fmt.Errorf("%w: this platform does not run a session helper", ErrUnsupported)
}
