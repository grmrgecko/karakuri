//go:build darwin

package main

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func desktopEnvironment(ctx context.Context) ([]string, error) {
	return mergeDesktopEnvironment(nil, nil), nil
}
func configureUnixStartup(ctx context.Context, exe string, remove bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", "org.karakuri.auto.resident.plist")
	domain := "gui/" + strconv.Itoa(os.Geteuid())
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domain+"/org.karakuri.auto.resident").Run()
	if remove {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("resident LaunchAgent removed")
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	log := html.EscapeString(filepath.Join(config, "karakuri-auto", "resident.log"))
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>org.karakuri.auto.resident</string><key>ProgramArguments</key><array><string>%s</string><string>resident</string><string>serve</string></array><key>AbandonProcessGroup</key><true/><key>RunAtLoad</key><true/><key>LimitLoadToSessionType</key><string>Aqua</string><key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string></dict></plist>`, html.EscapeString(exe), log, log)
	if err = os.WriteFile(path, []byte(plist), 0600); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("installing resident LaunchAgent: %w: %s", err, out)
	}
	return nil
}

func watchDesktopSession(context.Context, context.CancelFunc) {}
