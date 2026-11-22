package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yahooPeer answers the Yahoo Finance routes a quote uses. The quote route
// wants the crumb the peer currently issues and answers 401 otherwise, and
// only it reports a market cap; the chart route needs no crumb.
type yahooPeer struct {
	mu       sync.Mutex
	crumb    string
	throttle bool
}

func (y *yahooPeer) set(crumb string, throttle bool) {
	y.mu.Lock()
	y.crumb, y.throttle = crumb, throttle
	y.mu.Unlock()
}

func (y *yahooPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	y.mu.Lock()
	crumb, throttle := y.crumb, y.throttle
	y.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/test/getcrumb":
		if throttle {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("Too Many Requests"))
			return
		}
		_, _ = w.Write([]byte(crumb))
	case r.URL.Path == "/v7/finance/quote":
		if r.URL.Query().Get("crumb") != crumb {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"finance":{"result":null,"error":{"code":"Unauthorized","description":"Invalid Crumb"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"quoteResponse":{"result":[{"symbol":"AAPL","regularMarketPrice":200,"marketCap":3000000000000}],"error":null}}`))
	case r.URL.Path == "/v8/finance/chart/AAPL":
		_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"AAPL","regularMarketPrice":200,"chartPreviousClose":190}}],"error":null}}`))
	default:
		// The cookie page.
		w.WriteHeader(http.StatusOK)
	}
}

// TestYahooCrumbIsFetchedAgain pins that a quote returns to the crumb-signed
// route once a crumb can be had. The provider fetched its crumb once per
// process and kept whatever came back: a 429 page from a throttled crumb
// route was stored as the crumb, and a crumb Yahoo later refused was never
// replaced. Either way every quote from then on took the chart fallback,
// which has no market cap, until the plugin restarted.
func TestYahooCrumbIsFetchedAgain(t *testing.T) {
	peer := &yahooPeer{}
	srv := httptest.NewServer(peer)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err, "the test server has a URL")

	retry := crumbRetryInterval
	crumbRetryInterval = 0
	t.Cleanup(func() { crumbRetryInterval = retry })

	p := NewYahooProvider()
	p.httpClient.Transport = rewriteHost{target: target}
	marketCap := func() int64 {
		t.Helper()
		q, err := p.FetchQuote(context.Background(), "AAPL")
		require.NoError(t, err, "a quote must come back by one route or the other")
		assert.Equal(t, 200.0, q.Price, "both routes report the price")
		return q.MarketCap
	}

	peer.set("abc123", true)
	assert.Zero(t, marketCap(), "with the crumb route throttled the quote comes from the chart route")

	peer.set("abc123", false)
	assert.Equal(t, int64(3000000000000), marketCap(), "a crumb that could not be fetched must be fetched again")

	peer.set("def456", false)
	assert.Zero(t, marketCap(), "a refused crumb falls back to the chart route for that quote")
	assert.Equal(t, int64(3000000000000), marketCap(), "a refused crumb must be replaced")
}
