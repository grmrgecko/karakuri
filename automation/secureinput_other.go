//go:build !windows

package automation

import "context"

// ServeSecureInputCommand exists on every platform so the hidden command
// compiles everywhere, and answers only that this platform has no secure
// console to type into. Windows is the one platform that starts the helper.
func ServeSecureInputCommand(ctx context.Context, opts Options) error {
	return ErrUnsupported
}
