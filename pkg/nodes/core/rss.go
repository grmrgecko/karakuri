package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/mmcdole/gofeed"
)

// RSSNode parses a syndication feed into items.
//
// It is a parser rather than a poller, which is the whole reason it is small:
// schedule fires, http-request fetches, this decodes, split fans the items out,
// and dedupe on the item's id drops everything already seen. Every part of that
// but the decode already existed, and a node that fetched on its own schedule
// would be a second, worse copy of the ones that did.
//
// RSS, Atom, and JSON Feed differ in field names for the same three ideas, so
// items come back normalized: id, title, link, published, updated, summary,
// content, author, categories. The parser reads whichever of the three the
// document is, which is reading a document's own declared format rather than
// guessing what a payload means.
type RSSNode struct{}

func (RSSNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "rss",
		Label:       "Feed",
		Folder:      flow.FolderParsers,
		Description: "Parses an RSS, Atom, or JSON feed into items",
		Icon:        "las la-rss",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortObject),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "output",
				Label:   "Parse into",
				Type:    flow.FieldSelect,
				Default: "feed",
				Options: []engine.Option{
					{Value: "feed", Label: "One message holding the feed and its items"},
					{Value: "items", Label: "One message holding just the items"},
					{Value: "messages", Label: "A message per item"},
				},
				Help: "A message per item fans out like a split, which is what a dedupe and an " +
					"action downstream want. The feed shape keeps the title and link of the source.",
			},
			{
				ID:          "limit",
				Label:       "Maximum items",
				Type:        flow.FieldString,
				Placeholder: "20",
				Help:        "Keeps the newest this many, in the order the feed lists them. Empty keeps them all.",
			},
		},
	}
}

// rssConfig is the decoded rss node configuration.
type rssConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	Output   string `json:"output"`
	Limit    string `json:"limit"`
}

// Execute parses the source feed document.
func (RSSNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg rssConfig
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

	feed, err := gofeed.NewParser().ParseString(text)
	if err != nil {
		return nil, fmt.Errorf("parsing the feed: %w", err)
	}

	limit, err := parseGroupCount(cfg.Limit)
	if err != nil {
		return nil, fmt.Errorf("maximum items: %w", err)
	}
	entries := feed.Items
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	items := make([]any, 0, len(entries))
	for _, item := range entries {
		items = append(items, rssItem(item))
	}

	switch cfg.Output {
	case "messages":
		return parserFanOut(msg, property, cfg.Target, items)
	case "items":
		out, err := parserResult(msg, property, cfg.Target, items)
		if err != nil {
			return nil, err
		}
		return engine.One("out", out), nil
	case "", "feed":
		out, err := parserResult(msg, property, cfg.Target, map[string]any{
			"title":       feed.Title,
			"link":        feed.Link,
			"description": feed.Description,
			"updated":     rssTime(feed.UpdatedParsed),
			"count":       float64(len(items)),
			"items":       items,
		})
		if err != nil {
			return nil, err
		}
		return engine.One("out", out), nil
	default:
		return nil, fmt.Errorf("unknown output %q", cfg.Output)
	}
}

// rssItem flattens one entry into the shape a flow reads, the same keys
// whichever of the three formats the document was.
func rssItem(item *gofeed.Item) map[string]any {
	// The id is what a dedupe downstream keys on. A feed that publishes no GUID
	// falls back to the link and then the title, because an item with no stable
	// identity would otherwise look new on every poll.
	id := strings.TrimSpace(item.GUID)
	if id == "" {
		id = strings.TrimSpace(item.Link)
	}
	if id == "" {
		id = strings.TrimSpace(item.Title)
	}

	out := map[string]any{
		"id":         id,
		"title":      item.Title,
		"link":       item.Link,
		"summary":    item.Description,
		"content":    item.Content,
		"published":  rssTime(item.PublishedParsed),
		"updated":    rssTime(item.UpdatedParsed),
		"categories": rssStrings(item.Categories),
	}
	if item.Author != nil {
		out["author"] = item.Author.Name
	} else {
		out["author"] = ""
	}
	if item.Image != nil {
		out["image"] = item.Image.URL
	}
	return out
}

// rssTime renders a feed timestamp, leaving it empty when the entry carried
// none rather than substituting now, which would make every item look fresh.
func rssTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// rssStrings converts a category list to the any-typed slice the rest of the
// engine works in.
func rssStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
