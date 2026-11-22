package newsfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const oneHeadline = `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Headline one</title><link>http://example.invalid/1</link></item></channel></rss>`

// TestOutageKeepsLastGoodHeadlines locks that a failing feed keeps its last
// good headlines, with the error beside them, for as long as it fails. Only
// the first failed refresh kept them. A failure is retried after a minute,
// so a wall panel lost its headlines one minute into any outage.
func TestOutageKeepsLastGoodHeadlines(t *testing.T) {
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(oneHeadline))
	}))
	defer srv.Close()
	feed := Feed{URL: srv.URL + "/feed", Title: "Test"}

	// expire ages the cached entry past both lifetimes, as time passing would.
	expire := func() {
		mu.Lock()
		e := cache[feed.URL]
		e.fetched = time.Now().Add(-2 * cacheTTL)
		cache[feed.URL] = e
		mu.Unlock()
	}

	items, err := Fetch(context.Background(), feed)
	require.NoError(t, err, "the feed answers")
	require.Len(t, items, 1, "the feed holds one headline")

	down.Store(true)
	for _, refresh := range []string{"first", "second"} {
		expire()
		items, err = Fetch(context.Background(), feed)
		assert.Error(t, err, "the %s failed refresh reports the failure", refresh)
		assert.Len(t, items, 1, "the %s failed refresh keeps the last good headline", refresh)
	}
}
