package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// binanceTicker24hr follows the response example in the Binance spot API
// reference for GET /api/v3/ticker/24hr, written out by hand rather than
// captured from the exchange. Its priceChange and
// priceChangePercent are measured from openPrice, the first trade of the
// window. prevClosePrice is the last trade before the window and is a
// different number.
const binanceTicker24hr = `{
  "symbol": "BNBBTC",
  "priceChange": "-94.99999800",
  "priceChangePercent": "-95.960",
  "weightedAvgPrice": "0.29628482",
  "prevClosePrice": "0.10002000",
  "lastPrice": "4.00000200",
  "lastQty": "200.00000000",
  "bidPrice": "4.00000000",
  "bidQty": "100.00000000",
  "askPrice": "4.00000200",
  "askQty": "100.00000000",
  "openPrice": "99.00000000",
  "highPrice": "100.00000000",
  "lowPrice": "0.10000000",
  "volume": "8913.30000000",
  "quoteVolume": "15.30000000",
  "openTime": 1499783499040,
  "closeTime": 1499869899040,
  "firstId": 28385,
  "lastId": 28460,
  "count": 76
}`

// TestBinanceChangeMatchesItsPercentage pins the 24-hour change to the
// ticker's priceChange. The change was worked out against prevClosePrice
// while the percentage beside it came from the ticker, so the two described
// different moves: for the reference example a rise of 3.9 next to a fall of
// 95.96 percent.
func TestBinanceChangeMatchesItsPercentage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("symbol") == "" {
			_, _ = w.Write([]byte("[" + binanceTicker24hr + "]"))
			return
		}
		_, _ = w.Write([]byte(binanceTicker24hr))
	}))
	t.Cleanup(srv.Close)
	p := NewBinanceProvider(srv.URL)

	quote, err := p.FetchQuote(context.Background(), "BNB/BTC")
	require.NoError(t, err, "the ticker must decode")
	// 4.00000200 last - 99.00000000 open = -94.99999800.
	assert.Equal(t, -94.999998, quote.Change24h, "the change is the ticker's own, measured from the window's open")
	assert.Equal(t, -95.96, quote.ChangePct24h, "the percentage is the ticker's own")

	trending, err := p.FetchTrending(context.Background(), 5, "BTC")
	require.NoError(t, err, "the ticker list must decode")
	require.Len(t, trending, 1, "the list holds the one BTC pair")
	assert.Equal(t, -94.999998, trending[0].Change24h, "a trending row reports the same change as a quote")
}
