package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
)

// Pagination modes.
const (
	paginateOff     = "off"
	paginateParam   = "param"
	paginateNextURL = "next_url"
	paginateLinkHdr = "link_header"
)

// maxPaginationPages is the ceiling on the configured page cap. Pagination is
// a loop driven by data the server controls, so an API that keeps answering
// "there is more" must run out of rope somewhere.
const maxPaginationPages = 1000

// httpPage is one response, kept apart from the message it becomes so the
// pagination loop can read the body and the headers before deciding whether to
// ask for another.
type httpPage struct {
	payload    any
	status     int
	headers    map[string]any
	url        string
	durationMs int64
	// requestURL is the URL the page was fetched from, with its credentials.
	// Relative next links resolve against it; url is the masked form shown in
	// metadata and must not be sent.
	requestURL *url.URL
}

// asMessage renders a page as the message shape the node emits, which is also
// what the stop expression and the next-URL path are evaluated against. Both
// see exactly what a downstream node would.
func (p httpPage) asMessage(base *engine.Msg) *engine.Msg {
	meta := nodeutil.CloneMeta(base.Meta)
	meta["status"] = p.status
	meta["headers"] = p.headers
	meta["url"] = p.url
	meta["duration_ms"] = p.durationMs
	return &engine.Msg{
		Payload: p.payload,
		Topic:   base.Topic,
		Meta:    meta,
		Group:   base.Group,
	}
}

// paginating reports whether the node fetches more than one page.
func (c httpRequestConfig) paginating() bool {
	switch c.Pagination {
	case paginateParam, paginateNextURL, paginateLinkHdr:
		return true
	default:
		return false
	}
}

// fetchPages runs the request once, or repeatedly when pagination is on.
//
// A retry starts from page one. Nothing is emitted until every requested page
// succeeds, so a failed later page cannot leave partial output downstream.
func fetchPages(ctx context.Context, ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg, client *http.Client) ([]httpPage, error) {
	limit, err := cfg.pageLimit()
	if err != nil {
		return nil, err
	}
	delay, err := parseDurationDefault(cfg.PageDelay, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid delay between pages %q: %w", cfg.PageDelay, err)
	}

	pages := make([]httpPage, 0, limit)
	nextURL := ""
	pageValue, err := cfg.pageStart()
	if err != nil {
		return nil, err
	}

	for len(pages) < limit {
		// Space the requests out. Parking rather than sleeping gives the worker
		// slot back, which matters when a slow paginated call would otherwise
		// hold one for a minute.
		if delay > 0 && len(pages) > 0 {
			if err := ec.Park(ctx, delay); err != nil {
				return nil, err
			}
		}

		page, err := fetchOnePage(ctx, ec, cfg, msg, client, nextURL, pageValue)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)

		if !cfg.paginating() {
			break
		}
		more, next, err := cfg.nextPage(ec, msg, page, &pageValue)
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
		nextURL = next
	}
	return pages, nil
}

// nextPage decides whether to ask for another page and, for next-URL mode, what
// to ask for. pageValue is advanced in place for parameter mode.
func (c httpRequestConfig) nextPage(ec engine.ExecContext, msg *engine.Msg, page httpPage, pageValue *int) (bool, string, error) {
	switch c.Pagination {
	case paginateNextURL, paginateLinkHdr:
		next, err := c.resolveNextURL(page)
		if err != nil {
			return false, "", err
		}
		return next != "", next, nil

	case paginateParam:
		done, err := c.stopHere(ec, msg, page)
		if err != nil {
			return false, "", err
		}
		if done {
			return false, "", nil
		}
		step, err := c.pageStepValue()
		if err != nil {
			return false, "", err
		}
		*pageValue += step
		return true, "", nil

	default:
		return false, "", nil
	}
}

// resolveNextURL reads the link to the following page, from the body or from a
// Link header. Which of the two is a separate pagination mode rather than a
// guess: both are common, an API can carry both, and making it one setting is
// also what lets the body path's field appear only when it applies.
func (c httpRequestConfig) resolveNextURL(page httpPage) (string, error) {
	var next string
	if c.Pagination == paginateLinkHdr {
		next = linkHeaderNext(page.headers)
	} else {
		var err error
		next, err = c.bodyNextURL(page)
		if err != nil {
			return "", err
		}
	}
	if next == "" {
		return "", nil
	}
	// A relative link is resolved against the page it came from, which is what
	// an API returning "?page=3" means by it.
	ref, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("invalid next page URL: check its syntax")
	}
	if page.requestURL == nil {
		return next, nil
	}
	return page.requestURL.ResolveReference(ref).String(), nil
}

// bodyNextURL reads the next link from the configured path in the page.
func (c httpRequestConfig) bodyNextURL(page httpPage) (string, error) {
	path := strings.TrimSpace(c.NextURLPath)
	if path == "" {
		return "", fmt.Errorf("next-page pagination needs the path to the next link")
	}
	probe := &engine.Msg{Payload: page.payload, Meta: map[string]any{
		"status": page.status, "headers": page.headers, "url": page.url,
	}}
	value, found := resolveProperty(probe, path)
	if !found || value == nil {
		// A missing link is how an API says there is nothing more, so this ends
		// the loop rather than failing it.
		return "", nil
	}
	return strings.TrimSpace(stringify(value)), nil
}

// stopHere reports whether parameter-mode pagination has reached the end.
func (c httpRequestConfig) stopHere(ec engine.ExecContext, msg *engine.Msg, page httpPage) (bool, error) {
	switch c.StopOn {
	case "", "empty":
		items, err := c.pageItems(page)
		if err != nil {
			return false, err
		}
		return len(items) == 0, nil

	case "expression":
		expr := strings.TrimSpace(c.StopExpression)
		if expr == "" {
			return false, fmt.Errorf("stopping on an expression needs an expression")
		}
		value, err := ec.Eval(expr, page.asMessage(msg))
		if err != nil {
			return false, fmt.Errorf("stop expression: %w", err)
		}
		return truthy(value), nil

	default:
		return false, fmt.Errorf("unknown stop condition %q", c.StopOn)
	}
}

// pageItems reads the list a page carries. An empty items path means the
// payload is itself the list, which is what an API returning a bare array does.
func (c httpRequestConfig) pageItems(page httpPage) ([]any, error) {
	path := strings.TrimSpace(c.ItemsPath)
	value := page.payload
	if path != "" {
		probe := &engine.Msg{Payload: page.payload, Meta: map[string]any{
			"status": page.status, "headers": page.headers, "url": page.url,
		}}
		found := false
		value, found = resolveProperty(probe, path)
		if !found {
			// A page that does not carry the list at all reads as the end,
			// which is how some APIs signal it rather than sending an empty one.
			return nil, nil
		}
	}
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("the items at %q are %T, not a list", path, value)
	}
	return items, nil
}

// pageLimit reads the page cap, which always applies.
func (c httpRequestConfig) pageLimit() (int, error) {
	if !c.paginating() {
		return 1, nil
	}
	n, err := parseGroupCount(c.MaxPages)
	if err != nil {
		return 0, fmt.Errorf("maximum pages: %w", err)
	}
	if n == 0 {
		n = 10
	}
	if n > maxPaginationPages {
		return 0, fmt.Errorf("maximum pages is capped at %d, got %d", maxPaginationPages, n)
	}
	return n, nil
}

// pageStart reads the first value of the page parameter.
func (c httpRequestConfig) pageStart() (int, error) {
	raw := strings.TrimSpace(c.PageStart)
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("first page %q is not a number", raw)
	}
	return n, nil
}

// pageStepValue reads how much the page parameter advances each time. A step of
// one is page numbering; a step of the page size is offset numbering.
func (c httpRequestConfig) pageStepValue() (int, error) {
	raw := strings.TrimSpace(c.PageStep)
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("page step %q is not a number", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("page step must be at least 1, got %d", n)
	}
	return n, nil
}

// linkHeaderNext pulls the rel="next" URL out of an RFC 8288 Link header, which
// is how GitHub and anything modelled on it paginate.
func linkHeaderNext(headers map[string]any) string {
	raw := ""
	for name, value := range headers {
		if strings.EqualFold(name, "Link") {
			raw, _ = value.(string)
			break
		}
	}
	if raw == "" {
		return ""
	}
	// Each entry is "<url>; rel=next". The entries are found by their angle
	// brackets instead of by splitting on commas, because a URL may hold commas.
	for {
		open := strings.Index(raw, "<")
		if open < 0 {
			return ""
		}
		length := strings.Index(raw[open:], ">")
		if length < 0 {
			return ""
		}
		link := strings.TrimSpace(raw[open+1 : open+length])
		var params string
		params, raw = cutLinkParams(raw[open+length+1:])
		for _, param := range strings.Split(params, ";") {
			name, value, found := strings.Cut(param, "=")
			if !found || strings.TrimSpace(strings.ToLower(name)) != "rel" {
				continue
			}
			// A rel may list several relation types separated by spaces.
			for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(value), `"'`)) {
				if strings.EqualFold(rel, "next") {
					return link
				}
			}
		}
	}
}

// cutLinkParams splits what follows a Link entry's URL into that entry's
// parameters and the rest of the header. The entry ends at the first comma
// outside a quoted parameter value.
func cutLinkParams(s string) (params, rest string) {
	quoted := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				return s[:i], s[i+1:]
			}
		}
	}
	return s, ""
}

// paginatedOutput assembles what the node emits after more than one page: the
// items of every page in one message, or a message per page.
func paginatedOutput(ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg, pages []httpPage) (map[string][]*engine.Msg, error) {
	last := pages[len(pages)-1]

	if cfg.PageOutput == "pages" {
		// A fresh group frame, so a join downstream recombines exactly these
		// pages and knows how many are coming.
		groupID := uuid.New().String()
		out := make([]*engine.Msg, 0, len(pages))
		for i, page := range pages {
			part := page.asMessage(msg)
			part.Meta["page"] = i + 1
			part.Meta["pages"] = len(pages)
			part.Group = msg.PushGroup(groupID, i, len(pages))
			out = append(out, part)
		}
		return map[string][]*engine.Msg{"out": out}, nil
	}

	// Merge: every page's items, concatenated in the order they were fetched.
	merged := make([]any, 0)
	for _, page := range pages {
		items, err := cfg.pageItems(page)
		if err != nil {
			return nil, err
		}
		merged = append(merged, items...)
	}

	out := last.asMessage(msg)
	out.Meta["pages"] = len(pages)
	path := strings.TrimSpace(cfg.ItemsPath)
	if path == "" {
		out.Payload = merged
		return engine.One("out", out), nil
	}
	// Writing the merged list back where it was read from keeps the envelope
	// the API sent, so a downstream path written against one page still works.
	if err := setProperty(out, path, merged); err != nil {
		return nil, fmt.Errorf("cannot write the merged items to %q: %w", path, err)
	}
	return engine.One("out", out), nil
}
