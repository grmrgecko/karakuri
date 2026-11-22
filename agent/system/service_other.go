//go:build !linux && !darwin && !windows

package system

import (
	"context"
	"fmt"
	"runtime"
)

func readServiceStatus(context.Context, string, string) (map[string]any, error) {
	return nil, fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}

func controlService(context.Context, string, string, string) error {
	return fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}

func readServiceEnablement(ctx context.Context, name, scope string) (map[string]any, error) {
	return readServiceStatus(ctx, name, scope)
}
