//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func desktopProcessEnvironments(ctx context.Context) ([][]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := [][]string{os.Environ()}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join("/proc", entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(path, "comm"))
		if strings.Contains(string(comm), "karakuri") || strings.TrimSpace(string(comm)) == "resident" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, "environ"))
		if err == nil {
			out = append(out, strings.Split(string(data), "\x00"))
		}
	}
	return out, nil
}
