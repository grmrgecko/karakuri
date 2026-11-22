// Package dialog asks the user something through a native dialog, on every
// platform, via ncruces/zenity (native Win32, osascript on macOS, zenity or
// qarma on Linux). A notification tells and a dialog asks, so cancelling is an
// answer of its own rather than a failure, and a dialog nobody reached in time
// times out rather than erroring.
package dialog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ncruces/zenity"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Backend implements autoapi.Dialog.
type Backend struct{}

// New returns the dialog backend.
func New() Backend { return Backend{} }

// Ask opens the dialog the request names and reports what the user did. The
// request's timeout, when set, bounds how long the dialog may stay open; a
// timeout comes back as TimedOut rather than as an error, since the caller
// asked and learned there was no one there.
func (Backend) Ask(ctx context.Context, req autoapi.DialogRequest) (autoapi.DialogResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return autoapi.DialogResult{}, errors.New("no message to show")
	}

	waiting := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		waiting, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	options := []zenity.Option{zenity.Context(waiting)}
	if req.Title != "" {
		options = append(options, zenity.Title(req.Title))
	}
	if req.OKLabel != "" {
		options = append(options, zenity.OKLabel(req.OKLabel))
	}
	if req.CancelLabel != "" {
		options = append(options, zenity.CancelLabel(req.CancelLabel))
	}

	value, answered, err := ask(req, options)
	if err != nil {
		// A dialog nobody answered in time is an outcome, not a failure: the
		// caller asked and got no answer, which is exactly what it needs to know.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(waiting.Err(), context.DeadlineExceeded) {
			return autoapi.DialogResult{TimedOut: true}, nil
		}
		return autoapi.DialogResult{}, err
	}
	return autoapi.DialogResult{Value: value, Answered: answered}, nil
}

// ask opens the dialog the kind names and reports the answer, whether the user
// confirmed, and any failure that was not them declining. Cancelling is an
// answer of its own, so it comes back as answered false rather than as an error.
func ask(req autoapi.DialogRequest, options []zenity.Option) (string, bool, error) {
	switch req.Kind {
	case autoapi.DialogInfo:
		return "", true, dismissed(zenity.Info(req.Text, options...))
	case autoapi.DialogWarning:
		return "", true, dismissed(zenity.Warning(req.Text, options...))
	case autoapi.DialogError:
		return "", true, dismissed(zenity.Error(req.Text, options...))

	case "", autoapi.DialogQuestion:
		err := zenity.Question(req.Text, options...)
		if errors.Is(err, zenity.ErrCanceled) {
			return "", false, nil
		}
		return "", err == nil, err

	case autoapi.DialogEntry, autoapi.DialogPassword:
		if req.Kind == autoapi.DialogPassword {
			options = append(options, zenity.HideText())
		} else if req.DefaultValue != "" {
			options = append(options, zenity.EntryText(req.DefaultValue))
		}
		value, err := zenity.Entry(req.Text, options...)
		if errors.Is(err, zenity.ErrCanceled) {
			return "", false, nil
		}
		return value, err == nil, err

	case autoapi.DialogList:
		choices := make([]string, 0, len(req.Choices))
		for _, choice := range req.Choices {
			if trimmed := strings.TrimSpace(choice); trimmed != "" {
				choices = append(choices, trimmed)
			}
		}
		if len(choices) == 0 {
			return "", false, errors.New("a list dialog needs at least one choice")
		}
		value, err := zenity.List(req.Text, choices, options...)
		if errors.Is(err, zenity.ErrCanceled) {
			return "", false, nil
		}
		return value, err == nil, err

	default:
		return "", false, fmt.Errorf("unknown dialog kind %q", req.Kind)
	}
}

// dismissed treats closing a dialog that only states something as reading it.
// There is nothing to decline on an information box, and a window manager that
// closes it counts the same as the button.
func dismissed(err error) error {
	if errors.Is(err, zenity.ErrCanceled) {
		return nil
	}
	return err
}
