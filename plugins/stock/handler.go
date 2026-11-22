package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/swaggest/usecase"
	"github.com/swaggest/usecase/status"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
)

// quoteInput defines the path parameters for the stock quote endpoint.
type quoteInput struct {
	Provider string `path:"provider" description:"Data provider name (e.g. yahoo, alphavantage, finnhub)."`
	Ticker   string `path:"ticker" description:"Stock ticker symbol (e.g. AAPL)."`
}

type quoteOutput = apiutil.Success[StockQuote]

func (p *StockPlugin) quoteInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in quoteInput, out *quoteOutput) error {
		ticker := strings.ToUpper(in.Ticker)

		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		// Check local cache first.
		cacheKey := string(tickerKey(in.Provider, ticker))
		if cached, found := p.cache.Get("quote:" + cacheKey); found {
			if quote, ok := cached.(*StockQuote); ok {
				out.Data = *quote
				return nil
			}
		}

		// Cache miss: perform an immediate fetch.
		quote, err := prov.FetchQuote(ctx, ticker)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		ttl := p.pollInterval * 2
		p.cache.Set("quote:"+cacheKey, quote, ttl)

		// Ensure polling is active for this ticker.
		p.scheduler.EnsurePolling(in.Provider, ticker)

		out.Data = *quote
		return nil
	})
	u.SetTitle("Get Stock Quote")
	u.SetDescription("Returns the current price quote for a stock ticker.")
	u.SetTags("Stock")
	return u
}

// ohlcInput defines the path and query parameters for the OHLC endpoint.
type ohlcInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Ticker   string `path:"ticker" description:"Stock ticker symbol."`
	Interval string `query:"interval" default:"1D" description:"Candle interval (e.g. 1D, 1W, 1M)."`
}

type ohlcOutput = apiutil.Success[OHLCData]

func (p *StockPlugin) ohlcInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ohlcInput, out *ohlcOutput) error {
		ticker := strings.ToUpper(in.Ticker)
		interval := in.Interval
		if interval == "" {
			interval = "1D"
		}

		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		ohlc, err := prov.FetchOHLC(ctx, ticker, interval)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		out.Data = *ohlc
		return nil
	})
	u.SetTitle("Get OHLC Data")
	u.SetDescription("Returns historical OHLC candlestick data for a stock ticker.")
	u.SetTags("Stock")
	return u
}

// searchInput defines the path and query parameters for the symbol search endpoint.
type searchInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Q        string `query:"q" required:"true" description:"Search query string."`
}

type searchOutput = apiutil.Success[[]SearchResult]

func (p *StockPlugin) searchInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in searchInput, out *searchOutput) error {
		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		results, err := prov.SearchSymbol(ctx, in.Q)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		out.Data = results
		return nil
	})
	u.SetTitle("Search Symbols")
	u.SetDescription("Searches for stock symbols by name or ticker.")
	u.SetTags("Stock")
	return u
}

// trendingInput defines the path and query parameters for the trending stocks endpoint.
type trendingInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Count    int    `query:"count" default:"10" description:"Number of trending stocks to return (max 50)."`
}

type trendingOutput = apiutil.Success[[]StockQuote]

func (p *StockPlugin) trendingInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in trendingInput, out *trendingOutput) error {
		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		count := in.Count
		if count <= 0 {
			count = 10
		}
		if count > 50 {
			count = 50
		}

		// Check cache first.
		cacheKey := in.Provider + ":trending:" + strconv.Itoa(count)
		if cached, found := p.cache.Get(cacheKey); found {
			if quotes, ok := cached.([]StockQuote); ok {
				out.Data = quotes
				return nil
			}
		}

		quotes, err := prov.FetchTrending(ctx, count)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		ttl := p.pollInterval * 2
		p.cache.Set(cacheKey, quotes, ttl)

		out.Data = quotes
		return nil
	})
	u.SetTitle("Get Trending Stocks")
	u.SetDescription("Returns trending stocks by activity or market interest.")
	u.SetTags("Stock")
	return u
}

func errProviderNotFound(name string) error {
	return statusError("unknown provider: " + name)
}

type statusError string

func (e statusError) Error() string { return string(e) }
