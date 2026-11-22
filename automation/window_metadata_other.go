//go:build windows || darwin

package automation

func enrichWindowBackend(w Window) Window { return w }

func rawPlatformWindow(w Window) Window { return w }
