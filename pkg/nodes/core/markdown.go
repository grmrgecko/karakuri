package core

import (
	"bytes"
	"context"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// MarkdownNode renders markdown to HTML.
//
// It exists because the things that produce markdown here are the things whose
// output someone reads: a model's answer, a note node, a script building a
// report. Getting that into an email body, a dashboard panel, or an HTTP
// response means HTML, and it is the same goldmark pipeline the built-in
// reference pages already go through, so a document renders the same wherever
// it is shown.
//
// The other direction is not here. HTML to markdown is a lossy guess at what
// the author meant rather than a conversion, it would pull in a dependency for
// the weaker half, and the html node already extracts what a flow actually
// wants out of a page.
type MarkdownNode struct{}

func (MarkdownNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "markdown",
		Label:       "Markdown",
		Folder:      flow.FolderParsers,
		Description: "Renders markdown text as HTML",
		Icon:        "lab la-markdown",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortString),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "tables",
				Label:   "GitHub extensions",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Tables, strikethrough, task lists, and bare URLs turned into links.",
			},
			{
				ID:      "unsafe_html",
				Label:   "Pass raw HTML through",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Off strips HTML written inside the markdown. Turn it on only for documents " +
					"this flow wrote: markdown from a model or a feed can carry a script tag.",
			},
			{
				ID:      "hard_wraps",
				Label:   "Line breaks are breaks",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "On renders every newline as a break, the way a chat message reads.",
			},
		},
	}
}

// markdownConfig is the decoded markdown node configuration.
type markdownConfig struct {
	Property   string `json:"property"`
	Target     string `json:"target"`
	Tables     *bool  `json:"tables"`
	UnsafeHTML bool   `json:"unsafe_html"`
	HardWraps  bool   `json:"hard_wraps"`
}

// Execute renders the source markdown to HTML.
func (MarkdownNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg markdownConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	text, err := parserText(value, property)
	if err != nil {
		return nil, err
	}

	var opts []goldmark.Option
	if cfg.Tables == nil || *cfg.Tables {
		opts = append(opts, goldmark.WithExtensions(extension.GFM))
	}
	var render []renderer.Option
	if cfg.UnsafeHTML {
		render = append(render, goldmarkhtml.WithUnsafe())
	}
	if cfg.HardWraps {
		render = append(render, goldmarkhtml.WithHardWraps())
	}
	if len(render) > 0 {
		opts = append(opts, goldmark.WithRendererOptions(render...))
	}

	var buf bytes.Buffer
	if err := goldmark.New(opts...).Convert([]byte(text), &buf); err != nil {
		return nil, err
	}
	out, err := parserResult(msg, property, cfg.Target, buf.String())
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}
