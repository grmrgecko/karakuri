package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/automation/autoapi"
)

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func selectedWindow(ctx context.Context, sess *automation.Session, match string) (automation.WindowInfo, error) {
	windows, err := sess.Window.List(ctx)
	if err != nil {
		return automation.WindowInfo{}, err
	}
	return screenshotWindow(windows, match, false)
}

func clickCommand(ctx context.Context, sess *automation.Session, args []string) error {
	if len(args) == 4 && args[0] == "--window" && args[2] == "--text" {
		w, err := selectedWindow(ctx, sess, args[1])
		if err != nil {
			return err
		}
		return clickControl(ctx, sess, w, args[3])
	}
	if len(args) != 0 && len(args) != 2 {
		return fmt.Errorf("usage: click [x y] | click --window <match> --text <label>")
	}
	if len(args) == 2 {
		x, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		y, err := strconv.Atoi(args[1])
		if err != nil {
			return err
		}
		if err = sess.Mouse.Move(ctx, x, y, false); err != nil {
			return err
		}
	}
	return sess.Mouse.Click(ctx, autoapi.ButtonLeft, 1)
}

func desktopCommand(ctx context.Context, s *automation.Session, cmd string, args []string) error {
	if cmd == "display" {
		if len(args) > 0 {
			if len(args) != 2 || args[0] != "set" {
				return fmt.Errorf("usage: display [set WIDTHxHEIGHTxDEPTH]")
			}
			parts := strings.Split(args[1], "x")
			if len(parts) != 3 {
				return fmt.Errorf("display mode must be WIDTHxHEIGHTxDEPTH")
			}
			var n [3]int
			for i, p := range parts {
				v, err := strconv.Atoi(p)
				if err != nil || v <= 0 {
					return fmt.Errorf("display dimensions and depth must be positive")
				}
				n[i] = v
			}
			if err := s.Inspection.SetDisplay(ctx, automation.DisplayMode{Width: n[0], Height: n[1], Depth: n[2]}); err != nil {
				return err
			}
		}
		displays, err := s.Inspection.Displays(ctx)
		if err != nil {
			return err
		}
		return printJSON(displays)
	}
	if cmd == "dialog" {
		if len(args) != 1 || args[0] != "list" {
			return fmt.Errorf("usage: dialog list")
		}
		result, err := s.Desktop.Dialogs(ctx, automation.DialogOptions{})
		if err != nil {
			return err
		}
		return printJSON(result)
	}
	if cmd == "menu" {
		if len(args) < 2 || (args[0] != "list" && args[0] != "invoke") {
			return fmt.Errorf("usage: menu list <window> | menu invoke <window> <path|id>")
		}
		w, err := selectedWindow(ctx, s, args[1])
		if err != nil {
			return err
		}
		if args[0] == "list" {
			if len(args) != 2 {
				return fmt.Errorf("usage: menu list <window>")
			}
			items, err := s.Desktop.Menus(ctx, w)
			if err != nil {
				return err
			}
			return printJSON(items)
		}
		if len(args) != 3 {
			return fmt.Errorf("usage: menu invoke <window> <path|id>")
		}
		return s.Desktop.InvokeMenu(ctx, w, args[2])
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: %s <window>", cmd)
	}
	w, err := selectedWindow(ctx, s, args[0])
	if err != nil {
		return err
	}
	if cmd == "controls" {
		controls, err := s.Desktop.Controls(ctx, w)
		if err != nil {
			return err
		}
		return printJSON(controls)
	}
	elements, err := s.Inspection.Elements(ctx, w)
	if err != nil {
		return err
	}
	return printJSON(elements)
}

func windowDetails(w automation.WindowInfo) string {
	enabled := "unknown"
	if w.Enabled != nil {
		enabled = strconv.FormatBool(*w.Enabled)
	}
	return fmt.Sprintf(" enabled=%s image=%q", enabled, w.Image)
}
func clickControl(ctx context.Context, s *automation.Session, w automation.WindowInfo, text string) error {
	if err := s.Desktop.ClickControl(ctx, w, automation.ControlTarget{Text: text}); err != nil {
		return err
	}
	fmt.Printf("clicked text=%q\n", text)
	return nil
}
