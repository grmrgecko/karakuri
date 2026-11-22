package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

// TestCoinbaseHistoryStaysWithinCandleLimit pins that every history timeline
// asks Coinbase for at most 300 candles per request, the most the exchange
// answers, and still returns the whole range in order. A year of daily
// candles went out as one request for 365 and came back as HTTP 400, so the
// one-year chart never loaded. One day, 288 five-minute candles, fits in a
// single request and must stay one.
func TestCoinbaseHistoryStaysWithinCandleLimit(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		granularity, err := strconv.ParseInt(q.Get("granularity"), 10, 64)
		if err != nil || granularity <= 0 {
			http.Error(w, `{"message":"bad granularity"}`, http.StatusBadRequest)
			return
		}
		start, errStart := time.Parse(time.RFC3339, q.Get("start"))
		end, errEnd := time.Parse(time.RFC3339, q.Get("end"))
		if errStart != nil || errEnd != nil {
			http.Error(w, `{"message":"bad range"}`, http.StatusBadRequest)
			return
		}
		if end.Unix()-start.Unix() > 300*granularity {
			http.Error(w, `{"message":"granularity too small for the requested time range. Count of aggregations requested exceeds 300"}`,
				http.StatusBadRequest)
			return
		}
		// The exchange answers newest first, one candle per bucket.
		var candles [][]float64
		for ts := end.Unix() / granularity * granularity; ts >= start.Unix(); ts -= granularity {
			candles = append(candles, []float64{float64(ts), 1, 3, 2, float64(ts), 10})
		}
		_ = json.NewEncoder(w).Encode(candles)
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err, "the test server URL must parse")

	p := NewCoinbaseProvider()
	p.httpClient.Transport = rewriteHost{target: target}

	cases := []struct {
		timeline     string
		wantRequests int
		minPoints    int
	}{
		{"1D", 1, 287},
		{"1Y", 2, 364},
	}
	for _, tc := range cases {
		t.Run(tc.timeline, func(t *testing.T) {
			requests = 0
			points, err := p.FetchHistory(context.Background(), "BTC/USD", tc.timeline)
			require.NoError(t, err, "a timeline the widget offers must load")
			assert.Equal(t, tc.wantRequests, requests, "the range must be split only as far as the candle limit needs")
			assert.GreaterOrEqual(t, len(points), tc.minPoints, "the whole range must come back, not its first window")
			for i := 1; i < len(points); i++ {
				require.Greater(t, points[i].Timestamp, points[i-1].Timestamp,
					"points must run oldest to newest with no candle repeated where two windows meet")
			}
		})
	}
}

// TestCoinbaseVolumeIsInQuoteCurrency pins that a Coinbase quote reports its
// 24 hour volume in the quote currency, as the other providers do. The
// exchange's stats endpoint counts volume in the base currency, and that
// number went out unconverted: the widget showed 10000 BTC of volume as
// "$10.0K", and trending ranked cheap coins first because they trade more
// units. 10000 BTC at 60000 USD is 600M USD. 1000000 DOGE at 0.1 USD is 100K
// USD, so BTC leads.
func TestCoinbaseVolumeIsInQuoteCurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/products":
			_, _ = w.Write([]byte(`[
				{"id":"DOGE-USD","base_currency":"DOGE","quote_currency":"USD","display_name":"DOGE/USD","status":"online"},
				{"id":"BTC-USD","base_currency":"BTC","quote_currency":"USD","display_name":"BTC/USD","status":"online"}]`))
		case strings.HasPrefix(r.URL.Path, "/products/BTC-USD/stats"):
			_, _ = w.Write([]byte(`{"open":"59000","high":"61000","low":"58000","volume":"10000","last":"60000"}`))
		case strings.HasPrefix(r.URL.Path, "/products/BTC-USD/ticker"):
			_, _ = w.Write([]byte(`{"price":"60000","volume":"10000"}`))
		case strings.HasPrefix(r.URL.Path, "/products/DOGE-USD/stats"):
			_, _ = w.Write([]byte(`{"open":"0.1","high":"0.11","low":"0.09","volume":"1000000","last":"0.1"}`))
		case strings.HasPrefix(r.URL.Path, "/products/DOGE-USD/ticker"):
			_, _ = w.Write([]byte(`{"price":"0.1","volume":"1000000"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	p := NewCoinbaseProvider()
	p.httpClient.Transport = rewriteHost{target: target}

	q, err := p.FetchQuote(context.Background(), "BTC/USD")
	require.NoError(t, err, "the quote must load")
	assert.InDelta(t, 600_000_000.0, q.Volume24h, 1, "10000 BTC at 60000 USD is 600M USD of volume")

	top, err := p.FetchTrending(context.Background(), 2, "USD")
	require.NoError(t, err, "trending must load")
	require.Len(t, top, 2, "both online USD products must be ranked")
	assert.Equal(t, "BTC/USD", top[0].Pair, "BTC traded 600M USD and must rank above DOGE at 100K USD")
	assert.Equal(t, "DOGE/USD", top[1].Pair, "DOGE must follow with the smaller quote volume")
}
