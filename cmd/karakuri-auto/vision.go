package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/grmrgecko/karakuri/automation"
)

func imageMatchCommand(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: image-match IMAGE REFERENCE [--minimum-match PERCENT] [--max-results N]")
	}
	opts := automation.ImageMatchOptions{MinimumMatch: 98, MaxResults: 10}
	for i := 2; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", args[i])
		}
		var err error
		switch args[i] {
		case "--minimum-match":
			opts.MinimumMatch, err = strconv.ParseFloat(args[i+1], 64)
		case "--max-results":
			opts.MaxResults, err = strconv.Atoi(args[i+1])
		default:
			return fmt.Errorf("unknown image-match option %q", args[i])
		}
		if err != nil {
			return err
		}
	}
	img, err := automation.ReadImage(args[0])
	if err != nil {
		return err
	}
	reference, err := automation.ReadImage(args[1])
	if err != nil {
		return err
	}
	result, err := automation.MatchImage(ctx, img, reference, opts)
	if err != nil {
		return err
	}
	return printJSON(result)
}
