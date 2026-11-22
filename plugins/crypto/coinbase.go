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

// CoinbaseProvider implements the Provider interface using the Coinbase public API.
type CoinbaseProvider struct {
	httpClient *http.Client
}

// NewCoinbaseProvider creates a Coinbase provider. No API key is required for public endpoints.
func NewCoinbaseProvider() *CoinbaseProvider {
	return &CoinbaseProvider{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchQuote retrieves the current price data for a trading pair (e.g. "BTC/USD").
func (p *CoinbaseProvider) FetchQuote(ctx context.Context, pair string) (*CryptoQuote, error) {
	base, quote := parsePair(pair)
	productID := strings.ToUpper(base) + "-" + strings.ToUpper(quote)

	// Fetch the 24hr stats and current ticker in parallel using two requests.
	statsURL := fmt.Sprintf("https://api.exchange.coinbase.com/products/%s/stats", url.PathEscape(productID))
	tickerURL := fmt.Sprintf("https://api.exchange.coinbase.com/products/%s/ticker", url.PathEscape(productID))

	statsBody, err := p.doRequest(ctx, statsURL)
	if err != nil {
		return nil, fmt.Errorf("fetching stats: %w", err)
	}

	tickerBody, err := p.doRequest(ctx, tickerURL)
	if err != nil {
		return nil, fmt.Errorf("fetching ticker: %w", err)
	}

	var stats cbStats
	if err := json.Unmarshal(statsBody, &stats); err != nil {
		return nil, fmt.Errorf("decoding stats: %w", err)
	}

	var ticker cbTicker
	if err := json.Unmarshal(tickerBody, &ticker); err != nil {
		return nil, fmt.Errorf("decoding ticker: %w", err)
	}

	price := cbFloat(ticker.Price)
	open := cbFloat(stats.Open)
	change := price - open
	var changePct float64
	if open != 0 {
		changePct = (change / open) * 100
	}

	// The stats endpoint counts volume in the base currency. Quotes carry it
	// in the quote currency, which is what trending ranks on.
	return &CryptoQuote{
		Pair:         normalizePair(base, quote),
		Base:         strings.ToUpper(base),
		Quote:        strings.ToUpper(quote),
		Price:        price,
		Change24h:    change,
		ChangePct24h: changePct,
		High24h:      cbFloat(stats.High),
		Low24h:       cbFloat(stats.Low),
		Volume24h:    cbFloat(stats.Volume) * price,
		Timestamp:    time.Now().Unix(),
		Name:         strings.ToUpper(base),
	}, nil
}

// SearchSymbol searches Coinbase products for matching trading pairs.
func (p *CoinbaseProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	body, err := p.doRequest(ctx, "https://api.exchange.coinbase.com/products")
	if err != nil {
		return nil, err
	}

	var products []cbProduct
	if err := json.Unmarshal(body, &products); err != nil {
		return nil, fmt.Errorf("decoding products: %w", err)
	}

	queryUpper := strings.ToUpper(query)
	results := make([]SearchResult, 0)

	for _, prod := range products {
		if prod.Status != "online" {
			continue
		}
		if !strings.Contains(strings.ToUpper(prod.BaseCurrency), queryUpper) &&
			!strings.Contains(strings.ToUpper(prod.ID), queryUpper) &&
			!strings.Contains(strings.ToUpper(prod.DisplayName), queryUpper) {
			continue
		}
		results = append(results, SearchResult{
			Pair:     prod.BaseCurrency + "/" + prod.QuoteCurrency,
			Base:     prod.BaseCurrency,
			Quote:    prod.QuoteCurrency,
			Name:     prod.DisplayName,
			Exchange: "Coinbase",
		})
		if len(results) >= 20 {
			break
		}
	}

	return results, nil
}

// FetchTrending returns the top N coins by volume on Coinbase, filtered to the given quote currency.
func (p *CoinbaseProvider) FetchTrending(ctx context.Context, n int, quoteCurrency string) ([]CryptoQuote, error) {
	body, err := p.doRequest(ctx, "https://api.exchange.coinbase.com/products")
	if err != nil {
		return nil, err
	}

	var products []cbProduct
	if err := json.Unmarshal(body, &products); err != nil {
		return nil, fmt.Errorf("decoding products: %w", err)
	}

	quoteUpper := strings.ToUpper(quoteCurrency)

	// Collect products matching the quote currency then fetch stats for each.
	type prodQuote struct {
		product cbProduct
		quote   *CryptoQuote
	}

	var candidates []prodQuote
	for _, prod := range products {
		if prod.Status != "online" || strings.ToUpper(prod.QuoteCurrency) != quoteUpper {
			continue
		}
		q, err := p.FetchQuote(ctx, prod.BaseCurrency+"/"+prod.QuoteCurrency)
		if err != nil {
			continue
		}
		candidates = append(candidates, prodQuote{product: prod, quote: q})
	}

	// Sort by volume descending.
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].quote.Volume24h > candidates[j-1].quote.Volume24h; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	if len(candidates) > n {
		candidates = candidates[:n]
	}

	coins := make([]CryptoQuote, 0, len(candidates))
	for i, c := range candidates {
		c.quote.Rank = i + 1
		coins = append(coins, *c.quote)
	}

	return coins, nil
}

// coinbaseMaxCandles is the most candles the exchange returns for one request.
const coinbaseMaxCandles = 300

// FetchHistory returns historical price points using Coinbase's candles endpoint.
func (p *CoinbaseProvider) FetchHistory(ctx context.Context, pair, timeline string) ([]PricePoint, error) {
	base, quote := parsePair(pair)
	productID := strings.ToUpper(base) + "-" + strings.ToUpper(quote)

	// Map timeline to granularity (seconds per candle) and lookback duration.
	var granularity int
	var lookback time.Duration
	switch timeline {
	case "1W":
		granularity = 3600
		lookback = 7 * 24 * time.Hour
	case "1M":
		granularity = 21600
		lookback = 30 * 24 * time.Hour
	case "3M":
		granularity = 86400
		lookback = 90 * 24 * time.Hour
	case "1Y":
		granularity = 86400
		lookback = 365 * 24 * time.Hour
	default: // 1D
		granularity = 300
		lookback = 24 * time.Hour
	}

	end := time.Now().UTC()
	start := end.Add(-lookback)

	// The exchange rejects a request spanning more than coinbaseMaxCandles
	// candles, so a longer range is read in windows, oldest first.
	window := time.Duration(coinbaseMaxCandles*granularity) * time.Second
	var points []PricePoint
	for from := start; from.Before(end); from = from.Add(window) {
		to := from.Add(window)
		if to.After(end) {
			to = end
		}
		u := fmt.Sprintf("https://api.exchange.coinbase.com/products/%s/candles?granularity=%d&start=%s&end=%s",
			url.PathEscape(productID), granularity,
			url.QueryEscape(from.Format(time.RFC3339)),
			url.QueryEscape(to.Format(time.RFC3339)))

		body, err := p.doRequest(ctx, u)
		if err != nil {
			return nil, err
		}

		// Coinbase returns [[timestamp, low, high, open, close, volume], ...] in reverse chronological order.
		var raw [][]float64
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decoding candles: %w", err)
		}

		for i := len(raw) - 1; i >= 0; i-- {
			candle := raw[i]
			if len(candle) < 5 {
				continue
			}
			ts := int64(candle[0])
			// Adjacent windows share their boundary candle.
			if len(points) > 0 && ts <= points[len(points)-1].Timestamp {
				continue
			}
			points = append(points, PricePoint{
				Timestamp: ts,
				Price:     candle[4], // Close price.
			})
		}
	}
	if points == nil {
		points = []PricePoint{}
	}

	return points, nil
}

func (p *CoinbaseProvider) doRequest(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinbase request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coinbase API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func cbFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Coinbase API response types.

type cbStats struct {
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Volume string `json:"volume"`
	Last   string `json:"last"`
}

type cbTicker struct {
	Price  string `json:"price"`
	Volume string `json:"volume"`
	Time   string `json:"time"`
}

type cbProduct struct {
	ID            string `json:"id"`
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
	DisplayName   string `json:"display_name"`
	Status        string `json:"status"`
}
