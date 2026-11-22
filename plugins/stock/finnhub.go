package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

// FinnhubProvider implements the Provider interface using Finnhub endpoints.
type FinnhubProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewFinnhubProvider creates a Finnhub provider with the given API key.
func NewFinnhubProvider(apiKey string) *FinnhubProvider {
	return &FinnhubProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchQuote retrieves the current quote for a symbol using the Finnhub quote endpoint.
func (p *FinnhubProvider) FetchQuote(ctx context.Context, symbol string) (*StockQuote, error) {
	params := url.Values{
		"symbol": {strings.ToUpper(symbol)},
	}

	body, err := p.doRequest(ctx, "/quote", params)
	if err != nil {
		return nil, err
	}

	var raw finnhubQuote
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	// Finnhub returns all zeros when a symbol is not found.
	if raw.Current == 0 && raw.PreviousClose == 0 {
		return nil, fmt.Errorf("no quote data for symbol %s", symbol)
	}

	return &StockQuote{
		Symbol:        strings.ToUpper(symbol),
		Name:          strings.ToUpper(symbol), // Finnhub quote does not return a name.
		Price:         raw.Current,
		Change:        raw.Change,
		ChangePercent: raw.ChangePercent,
		Open:          raw.Open,
		High:          raw.High,
		Low:           raw.Low,
		PreviousClose: raw.PreviousClose,
		Volume:        0, // Finnhub quote endpoint does not return volume.
		Timestamp:     raw.Timestamp,
		Currency:      "USD",
		Exchange:      "",
		MarketState:   "",
	}, nil
}

// FetchOHLC retrieves historical OHLC candle data for a symbol using the Finnhub stock candle endpoint.
func (p *FinnhubProvider) FetchOHLC(ctx context.Context, symbol, interval string) (*OHLCData, error) {
	resolution, from := finnhubMapInterval(interval)
	to := time.Now().Unix()

	params := url.Values{
		"symbol":     {strings.ToUpper(symbol)},
		"resolution": {resolution},
		"from":       {fmt.Sprintf("%d", from)},
		"to":         {fmt.Sprintf("%d", to)},
	}

	body, err := p.doRequest(ctx, "/stock/candle", params)
	if err != nil {
		return nil, err
	}

	var raw finnhubCandle
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if raw.Status == "no_data" || len(raw.Timestamps) == 0 {
		return &OHLCData{
			Symbol:   strings.ToUpper(symbol),
			Interval: interval,
			Candles:  []OHLCCandle{},
		}, nil
	}

	candles := make([]OHLCCandle, 0, len(raw.Timestamps))
	for i, ts := range raw.Timestamps {
		if i >= len(raw.Open) || i >= len(raw.High) || i >= len(raw.Low) || i >= len(raw.Close) {
			continue
		}
		var vol int64
		if i < len(raw.Volume) {
			vol = int64(raw.Volume[i])
		}
		candles = append(candles, OHLCCandle{
			Timestamp: ts,
			Open:      raw.Open[i],
			High:      raw.High[i],
			Low:       raw.Low[i],
			Close:     raw.Close[i],
			Volume:    vol,
		})
	}

	return &OHLCData{
		Symbol:   strings.ToUpper(symbol),
		Interval: interval,
		Candles:  candles,
	}, nil
}

// SearchSymbol queries Finnhub for symbol matches.
func (p *FinnhubProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	params := url.Values{
		"q": {query},
	}

	body, err := p.doRequest(ctx, "/search", params)
	if err != nil {
		return nil, err
	}

	var raw finnhubSearchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.Result))
	for _, r := range raw.Result {
		results = append(results, SearchResult{
			Symbol:   r.Symbol,
			Name:     r.Description,
			Type:     r.Type,
			Exchange: "",
		})
	}

	return results, nil
}

// FetchTrending returns trending US stocks by fetching active symbols and quoting the first N.
func (p *FinnhubProvider) FetchTrending(ctx context.Context, count int) ([]StockQuote, error) {
	params := url.Values{
		"exchange": {"US"},
	}

	body, err := p.doRequest(ctx, "/stock/symbol", params)
	if err != nil {
		return nil, err
	}

	var symbols []finnhubSymbol
	if err := json.Unmarshal(body, &symbols); err != nil {
		return nil, fmt.Errorf("decoding symbols: %w", err)
	}

	// Limit to count symbols and fetch quotes for each.
	if len(symbols) > count {
		symbols = symbols[:count]
	}

	quotes := make([]StockQuote, 0, len(symbols))
	for _, sym := range symbols {
		q, err := p.FetchQuote(ctx, sym.Symbol)
		if err != nil {
			continue
		}
		if q.Name == q.Symbol && sym.Description != "" {
			q.Name = sym.Description
		}
		quotes = append(quotes, *q)
	}

	return quotes, nil
}

// doRequest performs an HTTP GET to the Finnhub API at the given path with query parameters.
func (p *FinnhubProvider) doRequest(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := "https://finnhub.io/api/v1" + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	// Finnhub accepts the key in a header, which keeps it out of the URL and
	// so out of anything that records the URL.
	req.Header.Set("X-Finnhub-Token", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnhub request failed: %w", sdk.WithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// finnhubMapInterval maps widget interval names to Finnhub resolution strings and start times.
func finnhubMapInterval(interval string) (resolution string, from int64) {
	now := time.Now()
	switch interval {
	case "1D":
		return "5", now.AddDate(0, 0, -1).Unix()
	case "1W":
		return "30", now.AddDate(0, 0, -7).Unix()
	case "1M":
		return "D", now.AddDate(0, -1, 0).Unix()
	case "3M":
		return "D", now.AddDate(0, -3, 0).Unix()
	case "1Y":
		return "W", now.AddDate(-1, 0, 0).Unix()
	default:
		return "5", now.AddDate(0, 0, -1).Unix()
	}
}

// Finnhub API response types.

type finnhubQuote struct {
	Current       float64 `json:"c"`
	Change        float64 `json:"d"`
	ChangePercent float64 `json:"dp"`
	High          float64 `json:"h"`
	Low           float64 `json:"l"`
	Open          float64 `json:"o"`
	PreviousClose float64 `json:"pc"`
	Timestamp     int64   `json:"t"`
}

type finnhubCandle struct {
	Open       []float64 `json:"o"`
	High       []float64 `json:"h"`
	Low        []float64 `json:"l"`
	Close      []float64 `json:"c"`
	Volume     []float64 `json:"v"`
	Timestamps []int64   `json:"t"`
	Status     string    `json:"s"`
}

type finnhubSearchResponse struct {
	Count  int                  `json:"count"`
	Result []finnhubSearchMatch `json:"result"`
}

type finnhubSearchMatch struct {
	Description string `json:"description"`
	Symbol      string `json:"symbol"`
	Type        string `json:"type"`
}

type finnhubSymbol struct {
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	Type        string `json:"type"`
}
