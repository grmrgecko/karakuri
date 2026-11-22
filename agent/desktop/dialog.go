package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// A notification tells the user something and a dialog asks them. That is the
// difference worth having a second node for: a flow that needs a person to
// approve a step, pick one of a few options, or supply a value has nowhere else
// to get it, and the answer comes back as the payload rather than as a message
// nobody reads.

// Dialog kinds. The first four state something and are answered by dismissing
// them; the last three collect a value.
const (
	dialogInfo     = "info"
	dialogWarning  = "warning"
	dialogError    = "error"
	dialogQuestion = "question"
	dialogEntry    = "entry"
	dialogList     = "list"
)

// dialogDefaultTimeout is how long a dialog waits for a person before it gives
// up. A dialog holds the run that opened it, so an unanswered one on a screen
// nobody is at would otherwise hold it forever.
const dialogDefaultTimeout = 120 * time.Second

// dialogNode asks the agent user a question and reports the answer.
type dialogNode struct {
	sess    *automation.Session
	initErr error
}

func (dialogNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeDialog,
		Folder:      folder,
		Label:       "Ask",
		Description: "Opens a dialog in the agent user's session and reports what they answered",
		Icon:        "las la-comment-dots",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Answer", Type: flow.PortObject},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "kind",
				Label:   "Kind",
				Type:    flow.FieldSelect,
				Default: dialogQuestion,
				Options: []nodeapi.Option{
					{Value: dialogQuestion, Label: "Ask to confirm"},
					{Value: dialogEntry, Label: "Ask for a value"},
					{Value: dialogList, Label: "Ask to choose one"},
					{Value: dialogInfo, Label: "Tell them something"},
					{Value: dialogWarning, Label: "Warn them"},
					{Value: dialogError, Label: "Report a problem"},
				},
			},
			{
				ID:          "title",
				Label:       "Title",
				Type:        flow.FieldString,
				Placeholder: "Backup finished",
			},
			{
				ID:          "text",
				Label:       "Message",
				Type:        flow.FieldText,
				Required:    true,
				Placeholder: "Run the nightly backup now?",
				Help:        "{{ }} segments are rendered against the message.",
			},
			{
				ID:     "default_value",
				Label:  "Prefilled answer",
				Type:   flow.FieldString,
				ShowIf: &nodeapi.ShowIf{Field: "kind", Equals: dialogEntry},
			},
			{
				ID:      "choices",
				Label:   "Choices",
				Type:    flow.FieldJSON,
				Control: "list",
				ShowIf:  &nodeapi.ShowIf{Field: "kind", Equals: dialogList},
				Help:    "One entry per row. The chosen row comes back as the answer.",
			},
			{
				ID:          "ok_label",
				Label:       "Confirm button",
				Type:        flow.FieldString,
				Placeholder: "OK",
			},
			{
				ID:          "cancel_label",
				Label:       "Cancel button",
				Type:        flow.FieldString,
				Placeholder: "Cancel",
			},
			{
				ID:      "timeout",
				Label:   "Give up after",
				Type:    flow.FieldNumber,
				Default: 120,
				Help: "Seconds to wait for an answer. The run is held while the dialog is open, " +
					"so a screen nobody is at is what this is for.",
			},
		},
	}
}

// dialogConfig is the decoded dialog configuration.
type dialogConfig struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Text         string   `json:"text"`
	DefaultValue string   `json:"default_value"`
	Choices      []string `json:"choices"`
	OKLabel      string   `json:"ok_label"`
	CancelLabel  string   `json:"cancel_label"`
	Timeout      int      `json:"timeout"`
}

// Execute opens the dialog and waits for the answer.
func (n *dialogNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg dialogConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Kind == "password" {
		return nil, errors.New("secret answers are not supported because run history records node output")
	}
	msg := agentnode.PrimaryInput(inputs)

	text, err := agentnode.Interpolate(ec, cfg.Text, msg)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, errors.New("no message to show")
	}
	title, err := agentnode.Interpolate(ec, cfg.Title, msg)
	if err != nil {
		return nil, err
	}

	// A dialog holds the run that opened it, so it always carries a timeout: an
	// unanswered one on a screen nobody is at would otherwise hold the run open.
	timeout := dialogDefaultTimeout
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}

	result, err := n.sess.Dialog.Ask(ctx, automation.DialogRequest{
		Kind:         cfg.Kind,
		Title:        title,
		Text:         text,
		DefaultValue: cfg.DefaultValue,
		Choices:      cfg.Choices,
		OKLabel:      cfg.OKLabel,
		CancelLabel:  cfg.CancelLabel,
		Timeout:      timeout,
	})
	if err != nil {
		return nil, err
	}

	if result.TimedOut {
		// A dialog nobody answered in time is an outcome, not a failure: the
		// flow asked and got no answer, which is exactly what it needs to know.
		out := agentnode.PassThrough(msg, nil)
		out.Payload = map[string]any{"answered": false, "timed_out": true, "value": ""}
		return nodeapi.One("out", out), nil
	}

	out := agentnode.PassThrough(msg, map[string]any{"dialog": cfg.Kind})
	out.Payload = map[string]any{
		"answered":  result.Answered,
		"timed_out": false,
		"value":     result.Value,
	}
	return nodeapi.One("out", out), nil
}
