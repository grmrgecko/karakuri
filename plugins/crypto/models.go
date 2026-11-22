package main

// CryptoQuote holds real-time price data for a single trading pair.
type CryptoQuote struct {
	Pair          string    `json:"pair"`
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	Price         float64   `json:"price"`
	Change24h     float64   `json:"change_24h"`
	ChangePct24h  float64   `json:"change_pct_24h"`
	High24h       float64   `json:"high_24h"`
	Low24h        float64   `json:"low_24h"`
	Volume24h     float64   `json:"volume_24h"`
	MarketCap     float64   `json:"market_cap,omitempty"`
	Rank          int       `json:"rank,omitempty"`
	Timestamp     int64     `json:"timestamp"`
	Name          string    `json:"name,omitempty"`
	Image         string    `json:"image,omitempty"`
	SparklineData []float64 `json:"sparkline_data,omitempty"`
}

// PricePoint holds a single timestamp+price entry for history charts.
type PricePoint struct {
	Timestamp int64   `json:"t"`
	Price     float64 `json:"p"`
}

// SearchResult holds a symbol search result from a provider.
type SearchResult struct {
	Pair     string `json:"pair"`
	Base     string `json:"base"`
	Quote    string `json:"quote"`
	Name     string `json:"name"`
	Image    string `json:"image,omitempty"`
	Exchange string `json:"exchange,omitempty"`
}
