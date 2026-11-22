package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/browserprompt"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// BrowserPromptNode holds one message until a person chooses a response.
type BrowserPromptNode struct {
	manager *browserprompt.Manager
	lookup  func(string) (engine.NodeType, bool)
}

func (BrowserPromptNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: flow.NodeTypeBrowserPrompt, Label: "Browser prompt", Folder: flow.FolderUtility,
		Description: "Asks a person in an agent browser and continues through the chosen button output", Icon: "las la-comment-dots",
		DynamicPorts: true,
		Inputs:       []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:      flow.BrowserPromptPorts(json.RawMessage(`{}`)),
		ConfigSpec: []engine.FieldSpec{
			{ID: "agent", Label: "Agent", Type: flow.FieldString, Control: "agent-browser", Required: true, Help: "The agent must advertise Open URL and have an interactive desktop session."},
			{ID: "browser_base_url", Label: "Browser base URL", Type: flow.FieldString, Help: "Optional http or https server origin reachable from this agent, without a path. Defaults to server.base_url. Localhost refers to the agent's own machine."},
			{ID: "title", Label: "Title", Type: flow.FieldString, Default: "Help needed", Help: "Supports {{ }} expressions against the incoming message."},
			{ID: "markdown", Label: "Instructions", Type: flow.FieldMarkdown, AttachmentField: "attachments", Required: true, Help: "Markdown supports {{ }} expressions. Attach images or files in the editor."},
			{ID: "attachments", Label: "Attachments", Type: flow.FieldAttachments, ValueSchema: flow.PromptAttachmentsSchema(), NoParam: true},
			{ID: "buttons", Label: "Buttons", Type: flow.FieldButtons, Default: flow.DefaultPromptButtons(), ValueSchema: flow.PromptButtonsSchema(), NoParam: true, Help: "Each stable ID is an output. An unwired choice ends this branch. Retry must lead to a new action or run; a back-edge cannot rerun an already executed node."},
			{ID: "timeout", Label: "Answer timeout", Type: flow.FieldDuration, Default: "15m", Help: "1s through 24h. Timeout and browser launch failures route to Error. Parallel branches continue while this message waits."},
			{ID: "feedback", Label: "Written feedback", Type: flow.FieldBoolean, Default: false},
			{ID: "require_feedback", Label: "Require feedback", Type: flow.FieldBoolean, Default: false, ShowIf: &engine.ShowIf{Field: "feedback", Equals: true}},
		},
	}
}

func (n BrowserPromptNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg flow.BrowserPromptConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Buttons == nil {
		cfg.Buttons = flow.DefaultPromptButtons()
	}
	if cfg.Title == "" {
		cfg.Title = "Help needed"
	}
	cfg.RequireFeedback = cfg.Feedback && cfg.RequireFeedback
	if strings.TrimSpace(cfg.Markdown) == "" {
		return nil, fmt.Errorf("browser prompt needs instructions")
	}
	timeout, err := flow.PromptTimeout(cfg.Timeout)
	if err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	if input := inputs["in"]; input != nil {
		msg.ID = input.ID
	}
	cfg.Title, err = ec.Interpolate(cfg.Title, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering prompt title: %w", err)
	}
	cfg.Markdown, err = ec.Interpolate(cfg.Markdown, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering prompt instructions: %w", err)
	}
	if len(cfg.Markdown) > 64<<10 || len(cfg.Title) > 1024 {
		return nil, fmt.Errorf("browser prompt exceeds the 64 KiB instruction or 1024 byte title limit")
	}
	base := cfg.BrowserBaseURL
	if base == "" {
		base = config.C.Server.BaseURL
	}
	u, err := flow.PromptBaseURL(base)
	if err != nil {
		return nil, err
	}
	lookup := n.lookup
	if lookup == nil {
		lookup = engine.Default().Get
	}
	launcher, ok := lookup("agent." + cfg.Agent + ".open-url")
	if !ok {
		return nil, fmt.Errorf("selected agent is disconnected or does not provide Open URL")
	}
	manager := n.manager
	if manager == nil {
		manager = browserprompt.Default
	}
	token, request, err := manager.Create(ctx, cfg, timeout)
	if err != nil {
		return nil, err
	}
	defer manager.Release(token)
	u.Path = strings.TrimRight(u.Path, "/") + "/prompt"
	u.RawPath = ""
	u.Fragment = token
	launchCtx := promptLaunchContext{ExecContext: ec, url: u.String(), meta: launcher.Meta()}
	launchDeadline, cancelLaunch := context.WithTimeout(ctx, request.Remaining())
	_, launchErr := launcher.Execute(launchDeadline, launchCtx, map[string]*engine.Msg{"in": {}})
	cancelLaunch()
	if launchErr != nil {
		return nil, fmt.Errorf("opening browser prompt: %w", launchErr)
	}
	parker, ok := ec.(engine.SignalParker)
	if !ok {
		return nil, fmt.Errorf("browser prompt requires an interruptible server wait")
	}
	if remaining := request.Remaining(); remaining > 0 {
		if err := parker.ParkUntil(ctx, remaining, request.Wake()); err != nil {
			return nil, err
		}
	}
	response, err := request.Result()
	if err != nil {
		return nil, err
	}
	out := passThrough(msg)
	out.ID = msg.ID
	out.Continuation = msg.Continuation
	out.Meta["browser_prompt"] = map[string]any{"id": response.ID, "label": response.Label, "text": response.Text, "request_id": response.RequestID, "answered_at": response.AnsweredAt.Format(time.RFC3339Nano)}
	return engine.One(response.ID, out), nil
}

type promptLaunchContext struct {
	engine.ExecContext
	url  string
	meta engine.NodeMeta
}

func (c promptLaunchContext) Config(out any) error {
	raw, err := json.Marshal(map[string]string{"url": c.url})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (c promptLaunchContext) Ports() flow.PortSet {
	return flow.PortSet{Inputs: c.meta.Inputs, Outputs: c.meta.Outputs, Known: true}
}
func (c promptLaunchContext) Wired(string) bool        { return false }
func (c promptLaunchContext) Emit(string, *engine.Msg) {}
