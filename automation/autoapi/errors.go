package autoapi

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported reports that a capability cannot work on this session at all,
// as opposed to a tool being absent. A Wayland pointer position is the case:
// nothing installed would change the answer, so a caller should not suggest one.
var ErrUnsupported = errors.New("automation: capability not available on this session")

// missingToolError is the failure that means nothing installed can do the job.
// It is a type rather than a message because a caller with a second way to try
// checks for it before giving up: a Wayland screenshot helper that is not there
// is a reason to ask the X server, while a helper that ran and failed is not.
type missingToolError struct {
	action string
	names  []string
}

func (e missingToolError) Error() string {
	return fmt.Sprintf("cannot %s: install one of %s on this host", e.action, strings.Join(e.names, ", "))
}

// MissingTool reports that none of the tools that could do the job are
// installed, naming them so the operator knows what to add.
func MissingTool(action string, names ...string) error {
	return missingToolError{action: action, names: names}
}

// IsMissingTool reports whether a failure was nothing being installed.
func IsMissingTool(err error) bool {
	var missing missingToolError
	return errors.As(err, &missing)
}
