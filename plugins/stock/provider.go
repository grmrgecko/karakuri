package main

import "context"

// Provider abstracts a stock data source for quote retrieval, OHLC history, and symbol search.
type Provider interface {
	FetchQuote(ctx context.Context, symbol string) (*StockQuote, error)
	FetchOHLC(ctx context.Context, symbol, interval string) (*OHLCData, error)
	SearchSymbol(ctx context.Context, query string) ([]SearchResult, error)
	// FetchTrending returns the top N trending stocks for list/ticker layouts.
	FetchTrending(ctx context.Context, count int) ([]StockQuote, error)
}
