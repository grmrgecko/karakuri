package main

// StockQuote holds real-time quote data for a single ticker.
type StockQuote struct {
	Symbol        string  `json:"symbol"`
	Name          string  `json:"name"`
	Price         float64 `json:"price"`
	Change        float64 `json:"change"`
	ChangePercent float64 `json:"change_percent"`
	Open          float64 `json:"open"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	PreviousClose float64 `json:"previous_close"`
	Volume        int64   `json:"volume"`
	MarketCap     int64   `json:"market_cap,omitempty"`
	Timestamp     int64   `json:"timestamp"`
	Currency      string  `json:"currency"`
	Exchange      string  `json:"exchange"`
	MarketState   string  `json:"market_state"`
}

// OHLCCandle holds a single OHLC candlestick data point.
type OHLCCandle struct {
	Timestamp int64   `json:"t"`
	Open      float64 `json:"o"`
	High      float64 `json:"h"`
	Low       float64 `json:"l"`
	Close     float64 `json:"c"`
	Volume    int64   `json:"v"`
}

// OHLCData holds historical OHLC candle data for a symbol.
type OHLCData struct {
	Symbol   string       `json:"symbol"`
	Interval string       `json:"interval"`
	Candles  []OHLCCandle `json:"candles"`
}

// SearchResult holds a symbol search result.
type SearchResult struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Exchange string `json:"exchange"`
}
