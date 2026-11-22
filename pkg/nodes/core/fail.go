package core

import (
	"context"
	"errors"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// FailNode ends the run as a failure.
//
// Everything else here fails when something outside the flow went wrong: a
// request timed out, a file was missing, a payload was not the declared format.
// This is the author saying that a condition they checked for is a failure, so a
// switch that lands on the branch that should be impossible can say so instead
// of routing to nothing and letting the run finish green.
//
// It has no error branch. A node that exists to fail and then catches its own
// failure would be an expensive way to draw a wire, so the failure goes where an
// unhandled one goes: the node's retry policy first, then the flow's error flow,
// and the run ends failed either way. Putting it behind a retry is legitimate,
// which is why it stays retryable.
type FailNode struct{}

func (FailNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "fail",
		Label:       "Fail",
		Folder:      flow.FolderUtility,
		Description: "Ends the run as a failure with a message the author writes",
		Icon:        "las la-exclamation-triangle",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		// No outputs at all: nothing continues past this node, and an output
		// port would suggest otherwise on the canvas.
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "message",
				Label:       "Message",
				Type:        flow.FieldString,
				Required:    true,
				Default:     "the flow reached a state it treats as a failure",
				Placeholder: "{{ payload.sensor }} reported an impossible reading",
				Help: "What the run history and the error flow report. {{ }} segments are " +
					"evaluated against the message, so the failure can name the data that caused it.",
			},
		},
	}
}

// failConfig is the decoded fail node configuration.
type failConfig struct {
	Message string `json:"message"`
}

// Execute fails, interpolating the message so it can name what went wrong.
func (FailNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg failConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	text := strings.TrimSpace(cfg.Message)
	if text == "" {
		return nil, errors.New("the flow reached a state it treats as a failure")
	}
	// An expression that will not evaluate must not replace the author's
	// message with a complaint about itself: the run is failing either way, and
	// the message they wrote is the more useful of the two.
	if rendered, err := ec.Interpolate(text, msg); err == nil {
		text = rendered
	} else {
		ec.Logger().Debug("fail message left uninterpolated", zap.Error(err))
	}
	return nil, errors.New(text)
}
