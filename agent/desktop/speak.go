package desktop

import (
	"context"
	"errors"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// speakNode reads text out on the agent host. It is the announcement a
// notification cannot make: a machine in another room has a speaker and no one
// watching its screen, which is most of what a house automation wants to say.
type speakNode struct {
	sess    *automation.Session
	initErr error
}

func (speakNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeSpeak,
		Folder:      folder,
		Label:       "Speak",
		Description: "Reads text out through this agent host's speech synthesizer",
		Icon:        "las la-volume-up",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "text",
				Label:       "Text",
				Type:        flow.FieldText,
				Required:    true,
				Placeholder: "The washing machine has finished",
				Help:        "{{ }} segments are rendered against the message.",
			},
			{
				ID:          "voice",
				Label:       "Voice",
				Type:        flow.FieldString,
				Placeholder: "Samantha",
				Help:        "Name of a voice the host's synthesizer has installed. Empty uses its default.",
			},
			{
				ID:          "rate",
				Label:       "Words per minute",
				Type:        flow.FieldNumber,
				Placeholder: "175",
				Help: "Speaking pace. Every platform scales its own way from this, so the same " +
					"number sounds the same on each of them. Empty uses the synthesizer's default.",
			},
			{
				ID:      "wait",
				Label:   "Wait until it has finished",
				Type:    flow.FieldBoolean,
				Default: true,
				Help: "Off returns as soon as the speech starts, which is what two announcements " +
					"in a row want when the second is not a reply to the first.",
			},
		},
	}
}

// speakConfig is the decoded speech configuration. Wait is a pointer so an
// absent field keeps its default of on.
type speakConfig struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
	Rate  int    `json:"rate"`
	Wait  *bool  `json:"wait"`
}

// Execute speaks the text and passes the message through.
func (n *speakNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg speakConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	text, err := agentnode.Interpolate(ec, cfg.Text, msg)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, errors.New("no text to speak")
	}

	wait := cfg.Wait == nil || *cfg.Wait
	if err := n.sess.Speaker.Speak(ctx, automation.SpeakRequest{Text: text, Voice: cfg.Voice, Rate: cfg.Rate, Wait: wait}); err != nil {
		return nil, err
	}
	out := agentnode.PassThrough(msg, map[string]any{"spoke": text, "waited": wait})
	return nodeapi.One("out", out), nil
}
