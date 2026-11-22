// Package newsfeed fetches the RSS, Atom, and JSON feeds a dashboard's news
// widget names. Fetches are cached per URL, so every viewer of a wall panel
// shares one request to the publisher every few minutes.
package newsfeed

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// Limits on what one fetch may cost.
const (
	// MaxFeeds is how many feeds one widget may name.
	MaxFeeds     = 5
	fetchTimeout = 10 * time.Second
	maxBodyBytes = 2 << 20
	cacheTTL     = 5 * time.Minute
	// failureTTL keeps a failing feed from being retried by every viewer
	// that loads while it is down.
	failureTTL   = time.Minute
	maxItems     = 50
	maxTextBytes = 600
)

// Item is one headline.
type Item struct {
	Title       string `json:"title"`
	Link        string `json:"link,omitempty"`
	Description string `json:"description,omitempty"`
	Published   string `json:"published,omitempty"`
	Source      string `json:"source"`
}

// Feed is one configured source as a widget names it.
type Feed struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type entry struct {
	items   []Item
	err     error
	fetched time.Time
}

var (
	mu    sync.Mutex
	cache = map[string]entry{}
	// client follows a few redirects and nothing else; the timeout bounds a
	// publisher that accepts the connection and never answers.
	client = &http.Client{Timeout: fetchTimeout}
)

// Fetch returns a feed's items, from the cache when it is fresh.
func Fetch(ctx context.Context, f Feed) ([]Item, error) {
	mu.Lock()
	e, ok := cache[f.URL]
	mu.Unlock()
	if ok {
		ttl := cacheTTL
		if e.err != nil {
			ttl = failureTTL
		}
		if time.Since(e.fetched) < ttl {
			return label(e.items, f), e.err
		}
	}

	items, err := fetch(ctx, f.URL)
	mu.Lock()
	cache[f.URL] = entry{items: items, err: err, fetched: time.Now()}
	if err != nil && ok && len(e.items) > 0 {
		// Keep serving the last good headlines through an outage, with the
		// error beside them. Every failed retry carries them forward.
		cache[f.URL] = entry{items: e.items, err: err, fetched: time.Now()}
		items = e.items
	}
	mu.Unlock()
	return label(items, f), err
}

// label names each item's source by the widget's title for the feed, or the
// URL's host when it has none.
func label(items []Item, f Feed) []Item {
	source := strings.TrimSpace(f.Title)
	if source == "" {
		source = f.URL
		if i := strings.Index(source, "://"); i >= 0 {
			source = source[i+3:]
		}
		source, _, _ = strings.Cut(source, "/")
	}
	out := make([]Item, len(items))
	for i, item := range items {
		item.Source = source
		out[i] = item
	}
	return out
}

func fetch(ctx context.Context, url string) ([]Item, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("feed URL must be http or https")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("User-Agent", "Karakuri news widget")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetching the feed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the feed: %w", err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("feed is larger than %d bytes", maxBodyBytes)
	}
	parsed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return nil, fmt.Errorf("parsing the feed: %w", err)
	}

	items := make([]Item, 0, min(len(parsed.Items), maxItems))
	for _, it := range parsed.Items {
		if len(items) == maxItems {
			break
		}
		item := Item{
			Title:       plainText(it.Title),
			Link:        it.Link,
			Description: plainText(it.Description),
		}
		switch {
		case it.PublishedParsed != nil:
			item.Published = it.PublishedParsed.UTC().Format(time.RFC3339)
		case it.UpdatedParsed != nil:
			item.Published = it.UpdatedParsed.UTC().Format(time.RFC3339)
		}
		if item.Title == "" {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// plainText strips markup from a feed field. Descriptions are HTML in most
// feeds; a headline on a wall panel shows their words, not their images or
// links.
func plainText(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if len(s) > maxTextBytes {
		cut := maxTextBytes
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		s = strings.TrimSpace(s[:cut]) + "…"
	}
	return s
}

// utf8Start reports whether a byte begins a UTF-8 sequence, so a truncation
// never splits a character.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
