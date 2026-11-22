package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	binanceUSBaseURL     = "https://api.binance.us"
	binanceGlobalBaseURL = "%s"
)

// BinanceProvider implements the Provider interface using the Binance public API.
type BinanceProvider struct {
	httpClient *http.Client
	baseURL    string
}

// NewBinanceProvider creates a Binance provider. The baseURL defaults to Binance.US.
// Pass an explicit URL to use a different regional endpoint (e.g. api.binance.com).
func NewBinanceProvider(baseURL string) *BinanceProvider {
	if baseURL == "" {
		baseURL = binanceUSBaseURL
	}
	return &BinanceProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchQuote retrieves the current price data for a trading pair (e.g. "BTC/USDT").
func (p *BinanceProvider) FetchQuote(ctx context.Context, pair string) (*CryptoQuote, error) {
	base, quote := parsePair(pair)
	symbol := strings.ToUpper(base) + strings.ToUpper(quote)

	u := fmt.Sprintf("%s/api/v3/ticker/24hr?symbol=%s", p.baseURL, url.QueryEscape(symbol))

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	var raw binanceTicker
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	// priceChange and priceChangePercent are both measured from the window's
	// first trade. prevClosePrice is the trade before the window, so a change
	// worked out against it does not belong with the percentage.
	price := parseFloat64(raw.LastPrice)
	change := parseFloat64(raw.PriceChange)
	changePct := parseFloat64(raw.PriceChangePercent)

	return &CryptoQuote{
		Pair:         normalizePair(base, quote),
		Base:         strings.ToUpper(base),
		Quote:        strings.ToUpper(quote),
		Price:        price,
		Change24h:    change,
		ChangePct24h: changePct,
		High24h:      parseFloat64(raw.HighPrice),
		Low24h:       parseFloat64(raw.LowPrice),
		Volume24h:    parseFloat64(raw.QuoteVolume),
		Timestamp:    time.Now().Unix(),
		Name:         strings.ToUpper(base),
	}, nil
}

// SearchSymbol searches Binance exchange info for matching trading pairs.
func (p *BinanceProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	body, err := p.doRequest(ctx, p.baseURL+"/api/v3/exchangeInfo")
	if err != nil {
		return nil, err
	}

	var raw binanceExchangeInfo
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding exchange info: %w", err)
	}

	queryUpper := strings.ToUpper(query)
	results := make([]SearchResult, 0)

	for _, s := range raw.Symbols {
		if s.Status != "TRADING" {
			continue
		}
		// Match against base asset or the full symbol.
		if !strings.Contains(strings.ToUpper(s.BaseAsset), queryUpper) &&
			!strings.Contains(strings.ToUpper(s.Symbol), queryUpper) {
			continue
		}
		results = append(results, SearchResult{
			Pair:     s.BaseAsset + "/" + s.QuoteAsset,
			Base:     s.BaseAsset,
			Quote:    s.QuoteAsset,
			Name:     s.BaseAsset,
			Exchange: "Binance",
		})
		if len(results) >= 20 {
			break
		}
	}

	return results, nil
}

// FetchTrending returns the top N coins by volume on Binance, filtered to the given quote currency.
func (p *BinanceProvider) FetchTrending(ctx context.Context, n int, quoteCurrency string) ([]CryptoQuote, error) {
	body, err := p.doRequest(ctx, p.baseURL+"/api/v3/ticker/24hr")
	if err != nil {
		return nil, err
	}

	var tickers []binanceTicker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return nil, fmt.Errorf("decoding tickers: %w", err)
	}

	// Filter to the requested quote currency and sort by volume descending.
	quoteSuffix := strings.ToUpper(quoteCurrency)
	type tickerWithVol struct {
		ticker binanceTicker
		vol    float64
	}

	var filtered []tickerWithVol
	for _, t := range tickers {
		if !strings.HasSuffix(t.Symbol, quoteSuffix) {
			continue
		}
		vol := parseFloat64(t.QuoteVolume)
		filtered = append(filtered, tickerWithVol{ticker: t, vol: vol})
	}

	// Simple insertion sort for top N by volume.
	for i := 1; i < len(filtered); i++ {
		for j := i; j > 0 && filtered[j].vol > filtered[j-1].vol; j-- {
			filtered[j], filtered[j-1] = filtered[j-1], filtered[j]
		}
	}

	if len(filtered) > n {
		filtered = filtered[:n]
	}

	coins := make([]CryptoQuote, 0, len(filtered))
	for i, f := range filtered {
		t := f.ticker
		base := strings.TrimSuffix(t.Symbol, quoteSuffix)
		price := parseFloat64(t.LastPrice)
		change := parseFloat64(t.PriceChange)
		changePct := parseFloat64(t.PriceChangePercent)

		coins = append(coins, CryptoQuote{
			Pair:         base + "/" + quoteSuffix,
			Base:         base,
			Quote:        quoteSuffix,
			Price:        price,
			Change24h:    change,
			ChangePct24h: changePct,
			High24h:      parseFloat64(t.HighPrice),
			Low24h:       parseFloat64(t.LowPrice),
			Volume24h:    f.vol,
			Rank:         i + 1,
			Timestamp:    time.Now().Unix(),
			Name:         base,
		})
	}

	return coins, nil
}

// FetchHistory returns historical price points using Binance's klines endpoint.
func (p *BinanceProvider) FetchHistory(ctx context.Context, pair, timeline string) ([]PricePoint, error) {
	base, quote := parsePair(pair)
	symbol := strings.ToUpper(base) + strings.ToUpper(quote)

	// Map timeline to Binance kline interval and lookback duration.
	var interval string
	var lookback time.Duration
	switch timeline {
	case "1W":
		interval = "1h"
		lookback = 7 * 24 * time.Hour
	case "1M":
		interval = "4h"
		lookback = 30 * 24 * time.Hour
	case "3M":
		interval = "1d"
		lookback = 90 * 24 * time.Hour
	case "1Y":
		interval = "1d"
		lookback = 365 * 24 * time.Hour
	default: // 1D
		interval = "5m"
		lookback = 24 * time.Hour
	}

	startTime := time.Now().Add(-lookback).UnixMilli()

	u := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&startTime=%d&limit=1000",
		p.baseURL, url.QueryEscape(symbol), interval, startTime)

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	// Binance returns [[openTime, open, high, low, close, volume, closeTime, ...], ...].
	var raw [][]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding klines: %w", err)
	}

	points := make([]PricePoint, 0, len(raw))
	for _, kline := range raw {
		if len(kline) < 5 {
			continue
		}
		var openTimeMs int64
		if err := json.Unmarshal(kline[0], &openTimeMs); err != nil {
			continue
		}
		var closeStr string
		if err := json.Unmarshal(kline[4], &closeStr); err != nil {
			continue
		}
		closePrice := parseFloat64(closeStr)
		points = append(points, PricePoint{
			Timestamp: openTimeMs / 1000,
			Price:     closePrice,
		})
	}

	return points, nil
}

func (p *BinanceProvider) doRequest(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func parseFloat64(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Binance API response types.

type binanceTicker struct {
	Symbol             string `json:"symbol"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
}

type binanceExchangeInfo struct {
	Symbols []struct {
		Symbol     string `json:"symbol"`
		Status     string `json:"status"`
		BaseAsset  string `json:"baseAsset"`
		QuoteAsset string `json:"quoteAsset"`
	} `json:"symbols"`
}
