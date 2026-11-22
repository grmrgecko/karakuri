package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteHost sends every request to a local server, keeping its path and
// query, so a provider that names its own host can be run against a test peer.
type rewriteHost struct{ target *url.URL }

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = r.target.Scheme
	out.URL.Host = r.target.Host
	out.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(out)
}

// TestAlphaVantageOHLCCoversOnlyTheInterval pins that a chart interval
// returns candles for that interval and no more. Alpha Vantage has no range
// parameter: a series comes back as its latest 100 points or as its whole
// history, so a three-month chart drew twenty years of daily candles and a
// one-day chart ran into the day before. The newest candle is always kept,
// which is what a one-day chart shows over a weekend.
func TestAlphaVantageOHLCCoversOnlyTheInterval(t *testing.T) {
	now := time.Now().In(eastern)
	entry := map[string]string{"1. open": "1", "2. high": "3", "3. low": "0.5", "4. close": "2", "5. volume": "10"}

	// Two years of daily keys, two years of weekly keys, and ten days of
	// five-minute keys, each ending now.
	daily := map[string]any{}
	for d := 0; d < 730; d++ {
		daily[now.AddDate(0, 0, -d).Format("2006-01-02")] = entry
	}
	weekly := map[string]any{}
	for w := 0; w < 104; w++ {
		weekly[now.AddDate(0, 0, -7*w).Format("2006-01-02")] = entry
	}
	intraday := map[string]any{}
	last := now.Truncate(5 * time.Minute)
	for m := 0; m < 10*24*12; m++ {
		intraday[last.Add(time.Duration(-5*m)*time.Minute).Format("2006-01-02 15:04:05")] = entry
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		switch r.URL.Query().Get("function") {
		case "TIME_SERIES_DAILY":
			body = map[string]any{"Time Series (Daily)": daily}
		case "TIME_SERIES_WEEKLY":
			body = map[string]any{"Weekly Time Series": weekly}
		default:
			body = map[string]any{"Time Series (5min)": intraday}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err, "the test server URL must parse")

	p := NewAlphaVantageProvider("key")
	p.httpClient.Transport = rewriteHost{target: target}

	cases := []struct {
		interval string
		// oldest is the earliest a candle may be; atLeast is how many candles
		// the interval must still hold.
		oldest  time.Time
		atLeast int
	}{
		{"1W", now.AddDate(0, 0, -7), 12 * 24},
		{"1M", now.AddDate(0, -1, 0), 27},
		{"3M", now.AddDate(0, -3, 0), 88},
		{"1Y", now.AddDate(-1, 0, 0), 51},
	}
	for _, tc := range cases {
		t.Run(tc.interval, func(t *testing.T) {
			ohlc, err := p.FetchOHLC(context.Background(), "IBM", tc.interval)
			require.NoError(t, err, "the series must load")
			require.GreaterOrEqual(t, len(ohlc.Candles), tc.atLeast, "the interval's own candles must all be kept")
			// A daily key is midnight UTC of its date, up to a day before the
			// same instant in the market's zone.
			floor := tc.oldest.Add(-24 * time.Hour).Unix()
			assert.GreaterOrEqual(t, ohlc.Candles[0].Timestamp, floor,
				"no candle may be older than the interval the chart asked for")
			assert.Equal(t, 2.0, ohlc.Candles[len(ohlc.Candles)-1].Close, "the newest candle must be kept and decoded")
		})
	}

	t.Run("1D", func(t *testing.T) {
		ohlc, err := p.FetchOHLC(context.Background(), "IBM", "1D")
		require.NoError(t, err, "the series must load")
		require.NotEmpty(t, ohlc.Candles, "the latest session must be kept")
		day := time.Unix(ohlc.Candles[len(ohlc.Candles)-1].Timestamp, 0).In(eastern).Format("2006-01-02")
		for _, c := range ohlc.Candles {
			require.Equal(t, day, time.Unix(c.Timestamp, 0).In(eastern).Format("2006-01-02"),
				"a one-day chart holds the latest session only")
		}
	})
}
