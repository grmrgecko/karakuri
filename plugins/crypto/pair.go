package main

import "strings"

// parsePair splits a pair string like "BTC/USD" or "BTCUSD" into base and quote components.
func parsePair(pair string) (base, quote string) {
	pair = strings.ToUpper(strings.TrimSpace(pair))

	// Handle "BTC/USD" format.
	if idx := strings.Index(pair, "/"); idx > 0 {
		return pair[:idx], pair[idx+1:]
	}

	// Handle "BTC-USD" format.
	if idx := strings.Index(pair, "-"); idx > 0 {
		return pair[:idx], pair[idx+1:]
	}

	// Fallback: try common quote currency suffixes.
	for _, suffix := range []string{"USDT", "USDC", "USD", "EUR", "GBP", "BTC", "ETH", "BNB"} {
		if strings.HasSuffix(pair, suffix) && len(pair) > len(suffix) {
			return pair[:len(pair)-len(suffix)], suffix
		}
	}

	// Last resort: assume a three-character quote. Two symbols run together
	// are at least six characters; anything shorter is one symbol on its own,
	// such as DOGE, and is priced in US dollars below.
	if len(pair) >= 6 {
		return pair[:len(pair)-3], pair[len(pair)-3:]
	}

	return pair, "USD"
}

// normalizePair returns a canonical "BASE/QUOTE" format.
func normalizePair(base, quote string) string {
	return strings.ToUpper(base) + "/" + strings.ToUpper(quote)
}
