package main

import (
	"context"
	"github.com/grmrgecko/karakuri/automation"
)

func commandSession(ctx context.Context, opts automation.Options) (*automation.Session, error) {
	return automation.New(ctx, opts)
}
