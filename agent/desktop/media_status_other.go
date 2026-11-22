//go:build !linux && !freebsd && !darwin

package desktop

import (
	"context"
	"fmt"
	"runtime"
)

func readMediaStatus(context.Context, string) (map[string]any, error) {
	return nil, fmt.Errorf("media status is not available on %s", runtime.GOOS)
}
