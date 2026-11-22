//go:build !windows && !darwin && !linux

package main

import "context"

func desktopProcessEnvironments(ctx context.Context) ([][]string, error) {
	return freeBSDEnvironments(ctx)
}
