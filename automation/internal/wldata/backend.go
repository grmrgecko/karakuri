//go:build !windows && !darwin

package wldata

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Clip is the autoapi.Clipboard implementation over data-control. It holds
// nothing between calls: a read opens a connection and closes it, and a write
// leaves one connection alive holding the selection until the compositor says
// it has been replaced. That is the same bargain the X11 selection owner
// makes — a selection belongs to a live client, so what this agent copied
// lasts as long as the agent, and a clipboard manager is what carries it
// further.
type Clip struct{}

// New returns the data-control clipboard, or an error when the compositor
// does not serve the interface.
func New(ctx context.Context) (*Clip, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, err
	}
	c.close()
	return &Clip{}, nil
}

// Read returns the selection's text.
func (Clip) Read(ctx context.Context, sel autoapi.Selection) (string, error) {
	return Read(ctx, sel == autoapi.SelectionPrimary)
}

// Write puts text on the selection and keeps ownership until it is replaced.
func (Clip) Write(ctx context.Context, sel autoapi.Selection, text string) error {
	return Write(ctx, sel == autoapi.SelectionPrimary, text)
}
