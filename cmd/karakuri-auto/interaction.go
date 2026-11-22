package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/grmrgecko/karakuri/automation"
)

func dragCommand(ctx context.Context, s *automation.Session, args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("usage: drag X1 Y1 X2 Y2 [--duration seconds] [--button left|middle|right]")
	}
	var points [4]int
	for i := range points {
		n, err := strconv.Atoi(args[i])
		if err != nil {
			return err
		}
		points[i] = n
	}
	req := automation.DragRequest{StartX: points[0], StartY: points[1], EndX: points[2], EndY: points[3], Duration: time.Second / 2, Button: "left"}
	for i := 4; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", args[i])
		}
		switch args[i] {
		case "--duration":
			d, err := seconds(args[i+1])
			if err != nil {
				return err
			}
			req.Duration = d
		case "--button":
			req.Button = automation.Button(args[i+1])
		default:
			return fmt.Errorf("unknown drag option %q", args[i])
		}
	}
	return s.Desktop.Drag(ctx, req)
}

func windowCommand(ctx context.Context, s *automation.Session, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: window <activate|minimize|maximize|close|restore> TARGET | window place TARGET X Y WIDTH HEIGHT")
	}
	w, err := selectedWindow(ctx, s, args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "restore", "place":
		p := automation.WindowPlacement{Restore: true}
		if args[0] == "place" {
			if len(args) != 6 {
				return fmt.Errorf("usage: window place TARGET X Y WIDTH HEIGHT")
			}
			var n [4]int
			for i := range n {
				value, err := strconv.Atoi(args[i+2])
				if err != nil {
					return err
				}
				n[i] = value
			}
			p.Move, p.Resize = true, true
			p.X, p.Y, p.Width, p.Height = n[0], n[1], n[2], n[3]
		} else if len(args) != 2 {
			return fmt.Errorf("usage: window restore TARGET")
		}
		result, err := automation.PlaceAndObserveWindow(ctx, s, w, p)
		if err != nil {
			return err
		}
		return printJSON(result)
	default:
		if len(args) != 2 {
			return fmt.Errorf("usage: window ACTION TARGET")
		}
		if err = s.Window.Act(ctx, w, automation.WindowAction(args[0])); err != nil {
			return err
		}
		fmt.Printf("%s on %q (%s)\n", args[0], w.Title, w.ID)
		return nil
	}
}
