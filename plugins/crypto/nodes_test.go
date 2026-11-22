package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCryptoAbsoluteThresholdDefault pins an omitted threshold in absolute
// mode to the catalog default of 1. Read as 0, every poll fired, unchanged
// price included.
func TestCryptoAbsoluteThresholdDefault(t *testing.T) {
	var cfg cryptoNodeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"pair":"BTC-USD","threshold_mode":"absolute"}`), &cfg))

	moved, _, _ := priceMoved(100, 100, true, cfg)
	assert.False(t, moved, "an unchanged price must not fire")
	moved, _, _ = priceMoved(100, 101, true, cfg)
	assert.True(t, moved, "a move of exactly 1 clears the default threshold")
}

// TestCoinGeckoQuoteUsesTheQuoteCurrency pins the 24-hour change of a
// CoinGecko quote to the pair's own quote currency. The top-level
// price_change_24h fields are always in US dollars, so a BTC/EUR quote paired
// a euro price with a dollar change and the dollar percentage.
func TestCoinGeckoQuoteUsesTheQuoteCurrency(t *testing.T) {
	var raw cgCoinResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"name": "Bitcoin",
		"market_data": {
			"current_price": {"usd": 100000, "eur": 90000},
			"high_24h": {"usd": 101000, "eur": 91000},
			"low_24h": {"usd": 97000, "eur": 88000},
			"price_change_24h": 2000,
			"price_change_percentage_24h": 2.04,
			"price_change_24h_in_currency": {"usd": 2000, "eur": 900},
			"price_change_percentage_24h_in_currency": {"usd": 2.04, "eur": 1.01}
		}
	}`), &raw), "the coin response must decode")

	eur, err := mapCGQuote(&raw, "BTC", "EUR")
	require.NoError(t, err, "a listed quote currency must map")
	assert.Equal(t, 90000.0, eur.Price, "the price is read in the quote currency")
	assert.Equal(t, 900.0, eur.Change24h, "the change must be in euros like the price beside it")
	assert.Equal(t, 1.01, eur.ChangePct24h, "the percentage must be the euro move, which differs from the dollar one")

	usd, err := mapCGQuote(&raw, "BTC", "USD")
	require.NoError(t, err, "a listed quote currency must map")
	assert.Equal(t, 2000.0, usd.Change24h, "a dollar pair keeps the dollar change")
	assert.Equal(t, 2.04, usd.ChangePct24h, "a dollar pair keeps the dollar percentage")
}

// TestCoinGeckoQuoteRejectsUnknownCurrency pins that a quote currency
// CoinGecko does not price is an error. The price map has no entry for it, and
// the lookup read as a price of zero, which the price trigger then emitted as
// a real move.
func TestCoinGeckoQuoteRejectsUnknownCurrency(t *testing.T) {
	var raw cgCoinResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"name": "Bitcoin",
		"market_data": {"current_price": {"usd": 100000, "eur": 90000}}
	}`), &raw), "the coin response must decode")

	quote, err := mapCGQuote(&raw, "BTC", "USDT")
	assert.ErrorContains(t, err, "USDT", "a currency missing from the response must be named as the failure")
	assert.Nil(t, quote, "no quote may carry a price the response did not hold")
}

// TestParsePair pins how a pair string splits into base and quote. A symbol
// written alone is the base against US dollars; the three-letter split is for
// two symbols run together, and applied to a lone four-letter symbol it read
// DOGE as D against OGE, which no exchange lists.
func TestParsePair(t *testing.T) {
	cases := []struct {
		name, pair, base, quote string
	}{
		{"slash", "btc/usd", "BTC", "USD"},
		{"dash", "ETH-EUR", "ETH", "EUR"},
		{"joined with a known quote", "BTCUSDT", "BTC", "USDT"},
		{"joined with an unlisted quote", "BTCJPY", "BTC", "JPY"},
		{"four letters joined with an unlisted quote", "DOGEJPY", "DOGE", "JPY"},
		{"lone three-letter symbol", "SOL", "SOL", "USD"},
		{"lone symbol that is also a quote", "ETH", "ETH", "USD"},
		{"lone four-letter symbol", "DOGE", "DOGE", "USD"},
		{"lone five-letter symbol", " matic ", "MATIC", "USD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, quote := parsePair(tc.pair)
			assert.Equal(t, tc.base, base, "the base must be the whole symbol")
			assert.Equal(t, tc.quote, quote, "a pair with no quote is priced in US dollars")
		})
	}
}
