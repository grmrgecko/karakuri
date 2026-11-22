package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// HTMLNode pulls values out of an HTML document with a CSS selector. It only
// reads: there is no serializing direction, since a flow that builds HTML is
// better served by the template node.
type HTMLNode struct{}

func (HTMLNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "html",
		Label:       "HTML",
		Folder:      flow.FolderParsers,
		Description: "Extracts elements from an HTML document with a CSS selector",
		Icon:        "lab la-html5",
		Inputs:      converterInput(flow.PortString),
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject, TypeFrom: "in",
			// One message holds every match, so it is an array; a message per
			// match holds a single match, which is text unless every attribute
			// of the element was asked for. TypeFrom covers an off-payload
			// target, where the payload passes through untouched.
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{
						"result": {"messages"}, "output": {"attribute"}, "attribute": {""},
					}, Type: flow.PortObject},
					{Match: map[string][]string{"result": {"messages"}}, Type: flow.PortString},
				},
				Default:     flow.PortObject,
				PayloadPath: []string{"target", "property"},
			},
		}},
		Pure: true,
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "selector",
				Label:       "Selector",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "table.prices tr td:first-child",
				Help:        "A CSS selector, the same syntax a browser uses.",
			},
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "output",
				Label:   "Take",
				Type:    flow.FieldSelect,
				Default: "text",
				Options: []engine.Option{
					{Value: "text", Label: "The text of each element"},
					{Value: "html", Label: "The HTML inside each element"},
					{Value: "attribute", Label: "An attribute of each element"},
				},
			},
			{
				ID:          "attribute",
				Label:       "Attribute",
				Type:        flow.FieldString,
				Placeholder: "href",
				ShowIf:      &flow.ShowIf{Field: "output", Equals: "attribute"},
				Help: "Elements without the attribute are skipped. Empty returns every attribute " +
					"of each element as an object.",
			},
			{
				ID:      "result",
				Label:   "Return",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "One message holding every match"},
					{Value: "messages", Label: "A message per match"},
				},
				Help: "A message per match fans out like a split, so a join downstream can put " +
					"the matches back together.",
			},
		},
	}
}

// htmlConfig is the decoded html node configuration.
type htmlConfig struct {
	Selector  string `json:"selector"`
	Property  string `json:"property"`
	Target    string `json:"target"`
	Output    string `json:"output"`
	Attribute string `json:"attribute"`
	Result    string `json:"result"`
}

// Execute selects elements from the document and returns what each one holds.
func (HTMLNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg htmlConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	selector := strings.TrimSpace(cfg.Selector)
	if selector == "" {
		return nil, errors.New("html node has no selector")
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

	// Compile the selector rather than letting a typo match nothing silently.
	matcher, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("invalid selector %q: %w", selector, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("cannot read the HTML in %q: %w", property, err)
	}

	matches, err := htmlMatches(doc.FindMatcher(matcher), cfg)
	if err != nil {
		return nil, err
	}

	switch cfg.Result {
	case "", "array":
		out, err := parserResult(msg, property, cfg.Target, matches)
		if err != nil {
			return nil, err
		}
		return engine.One("out", out), nil

	case "messages":
		if len(matches) == 0 {
			// Nothing matched, so there is nothing downstream to run.
			ec.Debug(selector, "html selector matched nothing")
			return nil, nil
		}
		return parserFanOut(msg, property, cfg.Target, matches)

	default:
		return nil, fmt.Errorf("unknown return mode %q", cfg.Result)
	}
}

// htmlMatches renders each selected element the configured way.
func htmlMatches(sel *goquery.Selection, cfg htmlConfig) ([]any, error) {
	attribute := strings.TrimSpace(cfg.Attribute)
	out := make([]any, 0, sel.Length())
	var failure error

	sel.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		switch cfg.Output {
		case "", "text":
			out = append(out, s.Text())

		case "html":
			inner, err := s.Html()
			if err != nil {
				failure = fmt.Errorf("cannot render the matched element: %w", err)
				return false
			}
			out = append(out, inner)

		case "attribute":
			if attribute != "" {
				if value, ok := s.Attr(attribute); ok {
					out = append(out, value)
				}
				return true
			}
			node := s.Get(0)
			attrs := make(map[string]any, len(node.Attr))
			for _, a := range node.Attr {
				attrs[a.Key] = a.Val
			}
			out = append(out, attrs)

		default:
			failure = fmt.Errorf("unknown output mode %q", cfg.Output)
			return false
		}
		return true
	})
	if failure != nil {
		return nil, failure
	}
	return out, nil
}
