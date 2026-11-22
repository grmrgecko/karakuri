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
)

// CoinGeckoProvider implements the Provider interface using the CoinGecko API.
type CoinGeckoProvider struct {
	httpClient *http.Client
	apiKey     string
}

// NewCoinGeckoProvider creates a CoinGecko provider. An empty apiKey uses the free public API.
func NewCoinGeckoProvider(apiKey string) *CoinGeckoProvider {
	return &CoinGeckoProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *CoinGeckoProvider) baseURL() string {
	if p.apiKey != "" {
		return "https://pro-api.coingecko.com/api/v3"
	}
	return "https://api.coingecko.com/api/v3"
}

// FetchQuote retrieves the current price data for a trading pair (e.g. "BTC/USD").
func (p *CoinGeckoProvider) FetchQuote(ctx context.Context, pair string) (*CryptoQuote, error) {
	base, quote := parsePair(pair)
	coinID := coinGeckoID(base)

	u := fmt.Sprintf("%s/coins/%s?localization=false&tickers=false&community_data=false&developer_data=false&sparkline=true",
		p.baseURL(), url.PathEscape(coinID))

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	var raw cgCoinResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return mapCGQuote(&raw, base, quote)
}

// mapCGQuote converts a coin response into the quote for one pair. The
// response's top-level 24-hour change fields are in US dollars whatever the
// pair, so the change is read from the per-currency maps like the price.
func mapCGQuote(raw *cgCoinResponse, base, quote string) (*CryptoQuote, error) {
	quoteLower := strings.ToLower(quote)
	price, ok := raw.MarketData.CurrentPrice[quoteLower]
	if !ok {
		return nil, fmt.Errorf("coingecko has no %s price for %s", strings.ToUpper(quote), strings.ToUpper(base))
	}
	change24h, ok := raw.MarketData.PriceChange24hInCurrency[quoteLower]
	if !ok {
		change24h = raw.MarketData.PriceChange24h
	}
	changePct24h, ok := raw.MarketData.PriceChangePercentage24hInCurrency[quoteLower]
	if !ok {
		changePct24h = raw.MarketData.PriceChangePercentage24h
	}
	high24h := raw.MarketData.High24h[quoteLower]
	low24h := raw.MarketData.Low24h[quoteLower]
	volume24h := raw.MarketData.TotalVolume[quoteLower]
	marketCap := raw.MarketData.MarketCap[quoteLower]

	return &CryptoQuote{
		Pair:          normalizePair(base, quote),
		Base:          strings.ToUpper(base),
		Quote:         strings.ToUpper(quote),
		Price:         price,
		Change24h:     change24h,
		ChangePct24h:  changePct24h,
		High24h:       high24h,
		Low24h:        low24h,
		Volume24h:     volume24h,
		MarketCap:     marketCap,
		Rank:          raw.MarketCapRank,
		Timestamp:     time.Now().Unix(),
		Name:          raw.Name,
		Image:         raw.Image.Small,
		SparklineData: raw.MarketData.Sparkline7d.Price,
	}, nil
}

// SearchSymbol searches for coins matching a query string.
func (p *CoinGeckoProvider) SearchSymbol(ctx context.Context, query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/search?query=%s", p.baseURL(), url.QueryEscape(query))

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	var raw cgSearchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.Coins))
	for _, c := range raw.Coins {
		symbol := strings.ToUpper(c.Symbol)
		results = append(results, SearchResult{
			Pair:  symbol + "/USD",
			Base:  symbol,
			Quote: "USD",
			Name:  c.Name,
			Image: c.Thumb,
		})
	}

	return results, nil
}

// FetchTrending returns the top N coins by market cap.
func (p *CoinGeckoProvider) FetchTrending(ctx context.Context, n int, quoteCurrency string) ([]CryptoQuote, error) {
	quoteLower := strings.ToLower(quoteCurrency)
	u := fmt.Sprintf("%s/coins/markets?vs_currency=%s&order=market_cap_desc&per_page=%d&page=1&sparkline=true&price_change_percentage=24h",
		p.baseURL(), url.QueryEscape(quoteLower), n)

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	var raw []cgMarketCoin
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding market response: %w", err)
	}

	coins := make([]CryptoQuote, 0, len(raw))
	for _, c := range raw {
		symbol := strings.ToUpper(c.Symbol)
		var sparkline []float64
		if c.SparklineIn7d != nil {
			sparkline = c.SparklineIn7d.Price
		}
		coins = append(coins, CryptoQuote{
			Pair:          symbol + "/" + strings.ToUpper(quoteCurrency),
			Base:          symbol,
			Quote:         strings.ToUpper(quoteCurrency),
			Price:         c.CurrentPrice,
			Change24h:     c.PriceChange24h,
			ChangePct24h:  c.PriceChangePercentage24h,
			High24h:       c.High24h,
			Low24h:        c.Low24h,
			Volume24h:     c.TotalVolume,
			MarketCap:     c.MarketCap,
			Rank:          c.MarketCapRank,
			Timestamp:     time.Now().Unix(),
			Name:          c.Name,
			Image:         c.Image,
			SparklineData: sparkline,
		})
	}

	return coins, nil
}

// FetchHistory returns historical price points using CoinGecko's market_chart endpoint.
func (p *CoinGeckoProvider) FetchHistory(ctx context.Context, pair, timeline string) ([]PricePoint, error) {
	base, quote := parsePair(pair)
	coinID := coinGeckoID(base)
	quoteLower := strings.ToLower(quote)

	days := "1"
	switch timeline {
	case "1W":
		days = "7"
	case "1M":
		days = "30"
	case "3M":
		days = "90"
	case "1Y":
		days = "365"
	}

	u := fmt.Sprintf("%s/coins/%s/market_chart?vs_currency=%s&days=%s",
		p.baseURL(), url.PathEscape(coinID), url.QueryEscape(quoteLower), days)

	body, err := p.doRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Prices [][]float64 `json:"prices"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding market chart: %w", err)
	}

	points := make([]PricePoint, 0, len(raw.Prices))
	for _, entry := range raw.Prices {
		if len(entry) < 2 {
			continue
		}
		points = append(points, PricePoint{
			Timestamp: int64(entry[0]) / 1000, // CoinGecko returns milliseconds.
			Price:     entry[1],
		})
	}

	return points, nil
}

func (p *CoinGeckoProvider) doRequest(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coingecko request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coingecko API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// CoinGecko API response types.

type cgCoinResponse struct {
	ID            string `json:"id"`
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	MarketCapRank int    `json:"market_cap_rank"`
	Image         struct {
		Small string `json:"small"`
	} `json:"image"`
	MarketData struct {
		CurrentPrice             map[string]float64 `json:"current_price"`
		MarketCap                map[string]float64 `json:"market_cap"`
		TotalVolume              map[string]float64 `json:"total_volume"`
		High24h                  map[string]float64 `json:"high_24h"`
		Low24h                   map[string]float64 `json:"low_24h"`
		PriceChange24h           float64            `json:"price_change_24h"`
		PriceChangePercentage24h float64            `json:"price_change_percentage_24h"`
		// The in-currency maps carry the same change per quote currency.
		PriceChange24hInCurrency           map[string]float64 `json:"price_change_24h_in_currency"`
		PriceChangePercentage24hInCurrency map[string]float64 `json:"price_change_percentage_24h_in_currency"`
		Sparkline7d                        struct {
			Price []float64 `json:"price"`
		} `json:"sparkline_7d"`
	} `json:"market_data"`
}

type cgSearchResponse struct {
	Coins []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
		Thumb  string `json:"thumb"`
	} `json:"coins"`
}

type cgMarketCoin struct {
	ID                       string  `json:"id"`
	Symbol                   string  `json:"symbol"`
	Name                     string  `json:"name"`
	Image                    string  `json:"image"`
	CurrentPrice             float64 `json:"current_price"`
	MarketCap                float64 `json:"market_cap"`
	MarketCapRank            int     `json:"market_cap_rank"`
	TotalVolume              float64 `json:"total_volume"`
	High24h                  float64 `json:"high_24h"`
	Low24h                   float64 `json:"low_24h"`
	PriceChange24h           float64 `json:"price_change_24h"`
	PriceChangePercentage24h float64 `json:"price_change_percentage_24h"`
	SparklineIn7d            *struct {
		Price []float64 `json:"price"`
	} `json:"sparkline_in_7d"`
}

// coinGeckoID maps common ticker symbols to CoinGecko coin IDs. Falls back to lowercase symbol.
func coinGeckoID(symbol string) string {
	ids := map[string]string{
		"BTC":   "bitcoin",
		"ETH":   "ethereum",
		"BNB":   "binancecoin",
		"SOL":   "solana",
		"XRP":   "ripple",
		"ADA":   "cardano",
		"DOGE":  "dogecoin",
		"DOT":   "polkadot",
		"AVAX":  "avalanche-2",
		"MATIC": "matic-network",
		"LINK":  "chainlink",
		"LTC":   "litecoin",
		"UNI":   "uniswap",
		"ATOM":  "cosmos",
		"XLM":   "stellar",
		"ALGO":  "algorand",
		"FIL":   "filecoin",
		"NEAR":  "near",
		"APT":   "aptos",
		"ARB":   "arbitrum",
		"OP":    "optimism",
		"SUI":   "sui",
		"SHIB":  "shiba-inu",
		"TRX":   "tron",
		"USDT":  "tether",
		"USDC":  "usd-coin",
	}
	if id, ok := ids[strings.ToUpper(symbol)]; ok {
		return id
	}
	return strings.ToLower(symbol)
}
