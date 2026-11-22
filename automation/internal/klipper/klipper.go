//go:build unix && !darwin

// Package klipper reads and writes the clipboard on a KDE Plasma session through
// klipper's D-Bus interface, which needs no tool installed: Plasma runs klipper
// already, and a Wayland session exposes the clipboard no other way to a
// command-line caller. Only the ordinary clipboard is served; klipper has no
// interface for the primary selection, so that stays the wl-clipboard job.
package klipper

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/session"
)

const (
	service   = "org.kde.klipper"
	path      = "/klipper"
	iface     = "org.kde.klipper.klipper"
	getMethod = iface + ".getClipboardContents"
	setMethod = iface + ".setClipboardContents"
)

// Backend implements autoapi.Clipboard over klipper.
type Backend struct{}

// New returns the klipper clipboard backend.
func New() Backend { return Backend{} }

// Available reports whether klipper is on the session bus to talk to.
func Available() bool {
	return session.NameHasOwner(service)
}

// Read returns the clipboard's text. The primary selection is not klipper's to
// give, so it is refused rather than answered with the clipboard.
func (Backend) Read(ctx context.Context, sel autoapi.Selection) (string, error) {
	if sel == autoapi.SelectionPrimary {
		return "", autoapi.MissingTool("read the primary selection on Wayland", "wl-paste")
	}
	conn := session.SessionBus()
	if conn == nil {
		return "", autoapi.MissingTool("read the clipboard", "klipper (a running KDE Plasma session)")
	}
	var text string
	err := conn.Object(service, path).CallWithContext(ctx, getMethod, 0).Store(&text)
	if err != nil {
		return "", err
	}
	return text, nil
}

// Write puts text on the clipboard.
func (Backend) Write(ctx context.Context, sel autoapi.Selection, text string) error {
	if sel == autoapi.SelectionPrimary {
		return autoapi.MissingTool("write the primary selection on Wayland", "wl-copy")
	}
	conn := session.SessionBus()
	if conn == nil {
		return autoapi.MissingTool("write the clipboard", "klipper (a running KDE Plasma session)")
	}
	return conn.Object(service, path).CallWithContext(ctx, setMethod, 0, text).Err
}
