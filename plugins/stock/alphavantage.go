package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

// AlphaVantageProvider implements the Provider interface using Alpha Vantage endpoints.
type AlphaVantageProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewAlphaVantageProvider creates an Alpha Vantage provider with the given API key.
func NewAlphaVantageProvider(apiKey string) *AlphaVantageProvider {
	return &AlphaVantageProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchQuote retrieves the current quote for a symbol using the GLOBAL_QUOTE function.
func (p *AlphaVantageProvider) FetchQuote(ctx context.Context, symbol string) (*StockQuote, error) {
	params := url.Values{
		"function": {"GLOBAL_QUOTE"},
		"symbol":   {strings.ToUpper(symbol)},
		"apikey":   {p.apiKey},
	}

	body, err := p.doRequest(ctx, params)
	if err != nil {
		return nil, err
	}

	var raw avGlobalQuoteResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if raw.GlobalQuote.Symbol == "" {
		return nil, fmt.Errorf("no quote data for symbol %s", symbol)
	}

	q := raw.GlobalQuote
	return &StockQuote{
		Symbol:        q.Symbol,
		Name:          q.Symbol, // Alpha Vantage GLOBAL_QUOTE does not return a name.
		Price:         parseFloat(q.Price),
		Change:        parseFloat(q.Change),
		ChangePercent: parseFloat(strings.TrimSuffix(q.ChangePercent, "%")),
		Open:          parseFloat(q.Open),
		High:          parseFloat(q.High),
		Low:           parseFloat(q.Low),
		PreviousClose: parseFloat(q.PreviousClose),
		Volume:        parseInt(q.Volume),
		Timestamp:     time.Now().Unix(),
		Currency:      "USD",
		Exchange:      "",
		MarketState:   "",
	}, nil
}

// FetchOHLC retrieves historical OHLC candle data for a symbol.
func (p *AlphaVantageProvider) FetchOHLC(ctx context.Context, symbol, interval string) (*OHLCData, error) {
	function, outputSize := avMapInterval(interval)

	params := url.Values{
		"function":   {function},
		"symbol":     {strings.ToUpper(symbol)},
		"apikey":     {p.apiKey},
		"outputsize": {outputSize},
	}

	// Intraday requires an explicit interval parameter.
	if function == "TIME_SERIES_INTRADAY" {
		params.Set("interval", "5min")
	}

	body, err := p.doRequest(ctx, params)
	if err != nil {
		return nil, err
	}

	// Alpha Vantage returns different top-level keys depending on the function,
	// so we parse into a generic map first.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	// Find the time series key (it varies by function).
	var seriesRaw json.RawMessage
	for key, val := range raw {
		if strings.Contains(key, "Time Series") {
			seriesRaw = val
			break
		}
	}
	if seriesRaw == nil {
		return nil, fmt.Errorf("no time series data in response for %s", symbol)
	}

	var series map[string]avOHLCEntry
	if err := json.Unmarshal(seriesRaw, &series); err != nil {
		return nil, fmt.Errorf("decoding time series: %w", err)
	}

	// Sort timestamps chronologically.
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	candles := make([]OHLCCandle, 0, len(keys))
	for _, k := range keys {
		entry := series[k]
		ts := avParseTime(k)
		candles = append(candles, OHLCCandle{
			Timestamp: ts,
			Open:      parseFloat(entry.Open),
			High:      parseFloat(entry.High),
			Low:       parseFloat(entry.Low),
			Close:     parseFloat(entry.Close),
			Volume:    parseInt(entry.Volume),
		})
	}

	return &OHLCData{
		Symbol:   strings.ToUpper(symbol),
		Interval: interval,
		Candles:  avTrim(candles, interval, time.Now()),
	}, nil
}

// avTrim keeps the candles a chart interval covers, given candles sorted
// oldest first. The API takes no range: a series is its latest 100 points or
// its whole history, which for daily and weekly data is twenty years. One day
// is the latest session, whenever it was, so a weekend still shows Friday.
// The newest candle is always kept.
func avTrim(candles []OHLCCandle, interval string, now time.Time) []OHLCCandle {
	if len(candles) == 0 {
		return candles
	}
	var cutoff int64
	switch interval {
	case "1W":
		cutoff = now.AddDate(0, 0, -7).Unix()
	case "1M":
		cutoff = now.AddDate(0, -1, 0).Unix()
	case "3M":
		cutoff = now.AddDate(0, -3, 0).Unix()
	case "1Y":
		cutoff = now.AddDate(-1, 0, 0).Unix()
	default: // 1D
		last := time.Unix(candles[len(candles)-1].Timestamp, 0).In(eastern)
		cutoff = time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, eastern).Unix()
	}
	first := len(candles) - 1
	for i, c := range candles {
		if c.Timestamp >= cutoff {
			first = i
			break
		}
	}
	return candles[first:]
}

// SearchSymbol queries Alpha Vantage for symbol matches using the SYMBOL_SEARCH function.
func (p *AlphaVantageProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	params := url.Values{
		"function": {"SYMBOL_SEARCH"},
		"keywords": {query},
		"apikey":   {p.apiKey},
	}

	body, err := p.doRequest(ctx, params)
	if err != nil {
		return nil, err
	}

	var raw avSearchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.BestMatches))
	for _, m := range raw.BestMatches {
		results = append(results, SearchResult{
			Symbol:   m.Symbol,
			Name:     m.Name,
			Type:     m.Type,
			Exchange: m.Region,
		})
	}

	return results, nil
}

// FetchTrending returns the most actively traded stocks using the TOP_GAINERS_LOSERS function.
func (p *AlphaVantageProvider) FetchTrending(ctx context.Context, count int) ([]StockQuote, error) {
	params := url.Values{
		"function": {"TOP_GAINERS_LOSERS"},
		"apikey":   {p.apiKey},
	}

	body, err := p.doRequest(ctx, params)
	if err != nil {
		return nil, err
	}

	var raw avTopMoversResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding trending response: %w", err)
	}

	entries := raw.MostActivelyTraded
	if len(entries) > count {
		entries = entries[:count]
	}

	quotes := make([]StockQuote, 0, len(entries))
	for _, e := range entries {
		quotes = append(quotes, StockQuote{
			Symbol:        e.Ticker,
			Name:          e.Ticker,
			Price:         parseFloat(e.Price),
			Change:        parseFloat(e.ChangeAmount),
			ChangePercent: parseFloat(strings.TrimSuffix(e.ChangePercentage, "%")),
			Volume:        parseInt(e.Volume),
			Timestamp:     time.Now().Unix(),
			Currency:      "USD",
		})
	}

	return quotes, nil
}

// doRequest performs an HTTP GET to the Alpha Vantage API with the given query parameters.
func (p *AlphaVantageProvider) doRequest(ctx context.Context, params url.Values) ([]byte, error) {
	u := "https://www.alphavantage.co/query?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpha vantage request failed: %w", sdk.WithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpha vantage API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// avMapInterval maps widget interval names to Alpha Vantage function names and output sizes.
func avMapInterval(interval string) (function, outputSize string) {
	switch interval {
	case "1D":
		return "TIME_SERIES_INTRADAY", "compact"
	case "1W":
		return "TIME_SERIES_INTRADAY", "full"
	case "1M":
		return "TIME_SERIES_DAILY", "compact"
	case "3M":
		return "TIME_SERIES_DAILY", "full"
	case "1Y":
		return "TIME_SERIES_WEEKLY", "compact"
	default:
		return "TIME_SERIES_INTRADAY", "compact"
	}
}

// avParseTime parses a series key from Alpha Vantage into a Unix timestamp.
// Intraday keys are "2006-01-02 15:04:05" in US Eastern time, the zone the
// series reports itself in; daily and weekly keys are a bare "2006-01-02".
func avParseTime(s string) int64 {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, eastern); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Unix()
	}
	return 0
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func parseInt(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

// Alpha Vantage API response types.

type avGlobalQuoteResponse struct {
	GlobalQuote avGlobalQuote `json:"Global Quote"`
}

type avGlobalQuote struct {
	Symbol        string `json:"01. symbol"`
	Open          string `json:"02. open"`
	High          string `json:"03. high"`
	Low           string `json:"04. low"`
	Price         string `json:"05. price"`
	Volume        string `json:"06. volume"`
	LatestDay     string `json:"07. latest trading day"`
	PreviousClose string `json:"08. previous close"`
	Change        string `json:"09. change"`
	ChangePercent string `json:"10. change percent"`
}

type avOHLCEntry struct {
	Open   string `json:"1. open"`
	High   string `json:"2. high"`
	Low    string `json:"3. low"`
	Close  string `json:"4. close"`
	Volume string `json:"5. volume"`
}

type avSearchResponse struct {
	BestMatches []avSearchMatch `json:"bestMatches"`
}

type avSearchMatch struct {
	Symbol   string `json:"1. symbol"`
	Name     string `json:"2. name"`
	Type     string `json:"3. type"`
	Region   string `json:"4. region"`
	Currency string `json:"8. currency"`
}

type avTopMoversResponse struct {
	MostActivelyTraded []avTopMover `json:"most_actively_traded"`
}

type avTopMover struct {
	Ticker           string `json:"ticker"`
	Price            string `json:"price"`
	ChangeAmount     string `json:"change_amount"`
	ChangePercentage string `json:"change_percentage"`
	Volume           string `json:"volume"`
}
