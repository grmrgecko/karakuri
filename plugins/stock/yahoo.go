package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// crumbRetryInterval is how long a failed crumb fetch stands before a quote
// tries again, so a throttled crumb route is not asked on every poll.
var crumbRetryInterval = time.Minute

// YahooProvider implements the Provider interface using Yahoo Finance endpoints.
type YahooProvider struct {
	httpClient *http.Client

	mu sync.Mutex
	// crumb signs the quote route, empty until one has been fetched.
	crumb string
	// crumbTried is when a crumb was last asked for.
	crumbTried time.Time
}

// NewYahooProvider creates a Yahoo Finance provider.
func NewYahooProvider() *YahooProvider {
	jar, _ := cookiejar.New(nil)
	return &YahooProvider{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
	}
}

// ensureCrumb returns the crumb the quote route needs, fetching one when none
// is held. A failed fetch is tried again after crumbRetryInterval.
func (p *YahooProvider) ensureCrumb(ctx context.Context) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.crumb != "" {
		return p.crumb
	}
	if !p.crumbTried.IsZero() && time.Since(p.crumbTried) < crumbRetryInterval {
		return ""
	}
	p.crumbTried = time.Now()
	p.crumb = p.fetchCrumb(ctx)
	return p.crumb
}

// dropCrumb forgets a crumb Yahoo refused, so the next quote fetches another.
func (p *YahooProvider) dropCrumb(crumb string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.crumb == crumb {
		p.crumb = ""
		p.crumbTried = time.Time{}
	}
}

// fetchCrumb asks Yahoo for a crumb, returning empty when it gives none.
func (p *YahooProvider) fetchCrumb(ctx context.Context) string {
	// Visit fc.yahoo.com to obtain session cookies.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://fc.yahoo.com/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ""
	}
	_ = resp.Body.Close()

	// Fetch the crumb using the session cookies.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "https://query2.finance.yahoo.com/v1/test/getcrumb", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err = p.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	// A throttled or refused request answers with an error page, which is
	// not a crumb.
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// FetchQuote retrieves the current quote for a symbol. It tries the v7 quote
// endpoint first (which includes market cap), falling back to the chart endpoint.
func (p *YahooProvider) FetchQuote(ctx context.Context, symbol string) (*StockQuote, error) {
	// Try v7 quote endpoint with crumb for full data including market cap.
	if crumb := p.ensureCrumb(ctx); crumb != "" {
		quote, err := p.fetchQuoteV7(ctx, symbol, crumb)
		if err == nil {
			return quote, nil
		}
	}

	// Fall back to chart endpoint.
	return p.fetchQuoteFromChart(ctx, symbol)
}

// fetchQuoteV7 retrieves a quote using the v7 finance quote endpoint.
func (p *YahooProvider) fetchQuoteV7(ctx context.Context, symbol, crumb string) (*StockQuote, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v7/finance/quote?symbols=%s&crumb=%s",
		url.QueryEscape(strings.ToUpper(symbol)),
		url.QueryEscape(crumb),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching quote: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		p.dropCrumb(crumb)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw yahooQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if raw.QuoteResponse.Error != nil {
		return nil, fmt.Errorf("yahoo finance error: %s", raw.QuoteResponse.Error.Description)
	}
	if len(raw.QuoteResponse.Result) == 0 {
		return nil, fmt.Errorf("no results for symbol %s", symbol)
	}

	r := &raw.QuoteResponse.Result[0]
	return &StockQuote{
		Symbol:        r.Symbol,
		Name:          r.ShortName,
		Price:         r.RegularMarketPrice,
		Change:        r.RegularMarketChange,
		ChangePercent: r.RegularMarketChangePercent,
		Open:          r.RegularMarketOpen,
		High:          r.RegularMarketDayHigh,
		Low:           r.RegularMarketDayLow,
		PreviousClose: r.RegularMarketPreviousClose,
		Volume:        r.RegularMarketVolume,
		MarketCap:     r.MarketCap,
		Timestamp:     r.RegularMarketTime,
		Currency:      r.Currency,
		Exchange:      r.FullExchangeName,
		MarketState:   r.MarketState,
	}, nil
}

// fetchQuoteFromChart retrieves a quote using the v8 chart endpoint.
func (p *YahooProvider) fetchQuoteFromChart(ctx context.Context, symbol string) (*StockQuote, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?range=1d&interval=1m",
		url.PathEscape(strings.ToUpper(symbol)),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching quote: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if raw.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo finance error: %s", raw.Chart.Error.Description)
	}
	if len(raw.Chart.Result) == 0 {
		return nil, fmt.Errorf("no results for symbol %s", symbol)
	}

	return mapQuoteFromChart(&raw.Chart.Result[0]), nil
}

// mapQuoteFromChart extracts a StockQuote from the Yahoo chart API response.
func mapQuoteFromChart(result *yahooChartResult) *StockQuote {
	meta := result.Meta
	price := meta.RegularMarketPrice
	prevClose := meta.ChartPreviousClose

	change := price - prevClose
	var changePct float64
	if prevClose != 0 {
		changePct = (change / prevClose) * 100
	}

	return &StockQuote{
		Symbol:        meta.Symbol,
		Name:          meta.ShortName,
		Price:         price,
		Change:        change,
		ChangePercent: changePct,
		Open:          meta.RegularMarketDayOpen,
		High:          meta.RegularMarketDayHigh,
		Low:           meta.RegularMarketDayLow,
		PreviousClose: prevClose,
		Volume:        meta.RegularMarketVolume,
		Timestamp:     meta.RegularMarketTime,
		Currency:      meta.Currency,
		Exchange:      meta.ExchangeName,
		MarketState:   meta.MarketState,
	}
}

// FetchOHLC retrieves historical OHLC candle data for a symbol.
func (p *YahooProvider) FetchOHLC(ctx context.Context, symbol, interval string) (*OHLCData, error) {
	rangeStr, intervalStr := mapInterval(interval)

	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?range=%s&interval=%s",
		url.PathEscape(strings.ToUpper(symbol)),
		url.QueryEscape(rangeStr),
		url.QueryEscape(intervalStr),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching OHLC: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if raw.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo finance error: %s", raw.Chart.Error.Description)
	}
	if len(raw.Chart.Result) == 0 {
		return nil, fmt.Errorf("no results for symbol %s", symbol)
	}

	return mapOHLCFromChart(&raw.Chart.Result[0], interval), nil
}

// SearchSymbol queries Yahoo Finance for symbol matches.
func (p *YahooProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v1/finance/search?q=%s&quotesCount=10&newsCount=0",
		url.QueryEscape(query),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching symbols: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance search returned %d: %s", resp.StatusCode, string(body))
	}

	var raw yahooSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.Quotes))
	for _, q := range raw.Quotes {
		results = append(results, SearchResult{
			Symbol:   q.Symbol,
			Name:     q.ShortName,
			Type:     q.QuoteType,
			Exchange: q.Exchange,
		})
	}

	return results, nil
}

// FetchTrending returns trending US stocks using the Yahoo Finance trending endpoint.
func (p *YahooProvider) FetchTrending(ctx context.Context, count int) ([]StockQuote, error) {
	u := "https://query1.finance.yahoo.com/v1/finance/trending/US?count=50"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching trending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance trending API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw yahooTrendingResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding trending response: %w", err)
	}

	if raw.Finance.Error != nil {
		return nil, fmt.Errorf("yahoo finance trending error: %s", raw.Finance.Error.Description)
	}
	if len(raw.Finance.Result) == 0 {
		return nil, fmt.Errorf("no trending results")
	}

	// Collect symbols from the trending response, limited to count.
	symbols := make([]string, 0, count)
	for _, q := range raw.Finance.Result[0].Quotes {
		symbols = append(symbols, q.Symbol)
		if len(symbols) >= count {
			break
		}
	}

	// Batch-fetch quotes for the trending symbols.
	quotes := make([]StockQuote, 0, len(symbols))
	for _, sym := range symbols {
		q, err := p.FetchQuote(ctx, sym)
		if err != nil {
			continue
		}
		quotes = append(quotes, *q)
	}

	return quotes, nil
}

// mapInterval converts widget interval names to Yahoo Finance range and interval params.
func mapInterval(interval string) (rangeStr, intervalStr string) {
	switch interval {
	case "1D":
		return "1d", "5m"
	case "1W":
		return "5d", "30m"
	case "1M":
		return "1mo", "1d"
	case "3M":
		return "3mo", "1d"
	case "1Y":
		return "1y", "1wk"
	default:
		return "1d", "5m"
	}
}

// mapOHLCFromChart extracts OHLC candle data from the Yahoo chart API response.
func mapOHLCFromChart(result *yahooChartResult, interval string) *OHLCData {
	data := &OHLCData{
		Symbol:   result.Meta.Symbol,
		Interval: interval,
	}

	timestamps := result.Timestamp
	if len(result.Indicators.Quote) == 0 || len(timestamps) == 0 {
		return data
	}

	q := result.Indicators.Quote[0]
	candles := make([]OHLCCandle, 0, len(timestamps))

	for i, ts := range timestamps {
		// Skip entries with nil values (market gaps).
		if i >= len(q.Open) || i >= len(q.High) || i >= len(q.Low) || i >= len(q.Close) || i >= len(q.Volume) {
			continue
		}
		if q.Open[i] == nil || q.Close[i] == nil {
			continue
		}

		candles = append(candles, OHLCCandle{
			Timestamp: ts,
			Open:      derefFloat(q.Open[i]),
			High:      derefFloat(q.High[i]),
			Low:       derefFloat(q.Low[i]),
			Close:     derefFloat(q.Close[i]),
			Volume:    derefInt(q.Volume[i]),
		})
	}

	data.Candles = candles
	return data
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefInt(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// Yahoo Finance API response types.

type yahooChartResponse struct {
	Chart struct {
		Result []yahooChartResult `json:"result"`
		Error  *yahooError        `json:"error"`
	} `json:"chart"`
}

type yahooError struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type yahooChartResult struct {
	Meta       yahooChartMeta `json:"meta"`
	Timestamp  []int64        `json:"timestamp"`
	Indicators struct {
		Quote []yahooQuoteIndicator `json:"quote"`
	} `json:"indicators"`
}

type yahooChartMeta struct {
	Symbol               string  `json:"symbol"`
	ShortName            string  `json:"shortName"`
	Currency             string  `json:"currency"`
	ExchangeName         string  `json:"exchangeName"`
	RegularMarketPrice   float64 `json:"regularMarketPrice"`
	ChartPreviousClose   float64 `json:"chartPreviousClose"`
	RegularMarketDayHigh float64 `json:"regularMarketDayHigh"`
	RegularMarketDayLow  float64 `json:"regularMarketDayLow"`
	RegularMarketDayOpen float64 `json:"regularMarketDayOpen"`
	RegularMarketVolume  int64   `json:"regularMarketVolume"`
	RegularMarketTime    int64   `json:"regularMarketTime"`
	MarketState          string  `json:"marketState"`
}

type yahooQuoteIndicator struct {
	Open   []*float64 `json:"open"`
	High   []*float64 `json:"high"`
	Low    []*float64 `json:"low"`
	Close  []*float64 `json:"close"`
	Volume []*int64   `json:"volume"`
}

type yahooSearchResponse struct {
	Quotes []yahooSearchQuote `json:"quotes"`
}

type yahooSearchQuote struct {
	Symbol    string `json:"symbol"`
	ShortName string `json:"shortname"`
	QuoteType string `json:"quoteType"`
	Exchange  string `json:"exchange"`
}

type yahooTrendingResponse struct {
	Finance struct {
		Result []yahooTrendingResult `json:"result"`
		Error  *yahooError           `json:"error"`
	} `json:"finance"`
}

type yahooTrendingResult struct {
	Quotes []struct {
		Symbol string `json:"symbol"`
	} `json:"quotes"`
}

type yahooQuoteResponse struct {
	QuoteResponse struct {
		Result []yahooQuoteResult `json:"result"`
		Error  *yahooError        `json:"error"`
	} `json:"quoteResponse"`
}

type yahooQuoteResult struct {
	Symbol                     string  `json:"symbol"`
	ShortName                  string  `json:"shortName"`
	Currency                   string  `json:"currency"`
	FullExchangeName           string  `json:"fullExchangeName"`
	RegularMarketPrice         float64 `json:"regularMarketPrice"`
	RegularMarketChange        float64 `json:"regularMarketChange"`
	RegularMarketChangePercent float64 `json:"regularMarketChangePercent"`
	RegularMarketOpen          float64 `json:"regularMarketOpen"`
	RegularMarketDayHigh       float64 `json:"regularMarketDayHigh"`
	RegularMarketDayLow        float64 `json:"regularMarketDayLow"`
	RegularMarketPreviousClose float64 `json:"regularMarketPreviousClose"`
	RegularMarketVolume        int64   `json:"regularMarketVolume"`
	RegularMarketTime          int64   `json:"regularMarketTime"`
	MarketCap                  int64   `json:"marketCap"`
	MarketState                string  `json:"marketState"`
}
