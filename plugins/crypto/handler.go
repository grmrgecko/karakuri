package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/swaggest/usecase"
	"github.com/swaggest/usecase/status"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
)

// quoteInput defines the path and query parameters for the quote endpoint.
type quoteInput struct {
	Provider string `path:"provider" description:"Data provider name (e.g. binance, coinbase, coingecko)."`
	Base     string `path:"base" description:"Base currency symbol (e.g. BTC)."`
	Quote    string `path:"quote" description:"Quote currency symbol (e.g. USD)."`
}

type quoteOutput = apiutil.Success[CryptoQuote]

func (p *CryptoPlugin) quoteInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in quoteInput, out *quoteOutput) error {
		pair := strings.ToUpper(in.Base) + "/" + strings.ToUpper(in.Quote)

		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		// Check local cache first.
		cacheKey := string(pairKey(in.Provider, pair))
		if cached, found := p.cache.Get("quote:" + cacheKey); found {
			if quote, ok := cached.(*CryptoQuote); ok {
				out.Data = *quote
				return nil
			}
		}

		// Cache miss: perform an immediate fetch.
		quote, err := prov.FetchQuote(ctx, pair)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		ttl := p.pollInterval * 2
		p.cache.Set("quote:"+cacheKey, quote, ttl)
		p.scheduler.EnsurePolling(in.Provider, pair)

		out.Data = *quote
		return nil
	})
	u.SetTitle("Get Crypto Quote")
	u.SetDescription("Returns the current price quote for a cryptocurrency trading pair.")
	u.SetTags("Crypto")
	return u
}

// historyInput defines the path and query parameters for the price history endpoint.
type historyInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Base     string `path:"base" description:"Base currency symbol."`
	Quote    string `path:"quote" description:"Quote currency symbol."`
	Timeline string `query:"timeline" default:"1D" description:"History timeline (1D, 1W, 1M, 3M, 1Y)."`
}

type historyOutput = apiutil.Success[[]PricePoint]

func (p *CryptoPlugin) historyInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in historyInput, out *historyOutput) error {
		pair := strings.ToUpper(in.Base) + "/" + strings.ToUpper(in.Quote)

		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		timeline := in.Timeline
		if timeline == "" {
			timeline = "1D"
		}

		// Check cache first.
		cacheKey := "history:" + in.Provider + ":" + pair + ":" + timeline
		if cached, found := p.cache.Get(cacheKey); found {
			if pts, ok := cached.([]PricePoint); ok {
				out.Data = pts
				return nil
			}
		}

		points, err := prov.FetchHistory(ctx, pair, timeline)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		ttl := p.pollInterval * 2
		p.cache.Set(cacheKey, points, ttl)

		out.Data = points
		return nil
	})
	u.SetTitle("Get Price History")
	u.SetDescription("Returns historical price data for a cryptocurrency trading pair.")
	u.SetTags("Crypto")
	return u
}

// searchInput defines the path and query parameters for the symbol search endpoint.
type searchInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Q        string `query:"q" required:"true" description:"Search query string."`
}

type searchOutput = apiutil.Success[[]SearchResult]

func (p *CryptoPlugin) searchInteractor() usecase.Interactor {
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
	u.SetDescription("Searches for cryptocurrency trading pairs by name or symbol.")
	u.SetTags("Crypto")
	return u
}

// trendingInput defines the path and query parameters for the trending coins endpoint.
type trendingInput struct {
	Provider string `path:"provider" description:"Data provider name."`
	Count    int    `query:"count" default:"10" description:"Number of trending coins to return (max 50)."`
	Quote    string `query:"quote" default:"USD" description:"Quote currency for rankings."`
}

type trendingOutput = apiutil.Success[[]CryptoQuote]

func (p *CryptoPlugin) trendingInteractor() usecase.Interactor {
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
		quoteCurrency := in.Quote
		if quoteCurrency == "" {
			quoteCurrency = "USD"
		}

		// Check cache first.
		cacheKey := in.Provider + ":trending:" + quoteCurrency + ":" + strconv.Itoa(count)
		if cached, found := p.cache.Get(cacheKey); found {
			if coins, ok := cached.([]CryptoQuote); ok {
				out.Data = coins
				return nil
			}
		}

		coins, err := prov.FetchTrending(ctx, count, quoteCurrency)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		ttl := p.pollInterval * 2
		p.cache.Set(cacheKey, coins, ttl)

		out.Data = coins
		return nil
	})
	u.SetTitle("Get Trending Coins")
	u.SetDescription("Returns trending cryptocurrencies by market cap.")
	u.SetTags("Crypto")
	return u
}

func errProviderNotFound(name string) error {
	return statusError("unknown provider: " + name)
}

type statusError string

func (e statusError) Error() string { return string(e) }
