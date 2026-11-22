package main

import "context"

// Provider abstracts a crypto data source for quote retrieval and symbol search.
type Provider interface {
	FetchQuote(ctx context.Context, pair string) (*CryptoQuote, error)
	SearchSymbol(ctx context.Context, query string) ([]SearchResult, error)
	// FetchTrending returns the top N coins by market cap for trending layouts.
	FetchTrending(ctx context.Context, n int, quoteCurrency string) ([]CryptoQuote, error)
	// FetchHistory returns historical price points for a trading pair over the given timeline.
	FetchHistory(ctx context.Context, pair, timeline string) ([]PricePoint, error)
}
