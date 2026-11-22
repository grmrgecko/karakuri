package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// folderCrypto is where the plugin's nodes sit in the palette.
const folderCrypto = "Crypto"

// cryptoProviderField is the exchange picker both crypto nodes share.
func cryptoProviderField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "provider", Label: "Exchange", Type: flow.FieldSelect, Default: "coinbase",
		Options: []engine.Option{
			{Value: "coinbase", Label: "Coinbase"},
			{Value: "binance", Label: "Binance"},
			{Value: "coingecko", Label: "CoinGecko"},
		},
	}
}

func cryptoPairField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "pair", Label: "Pair", Type: flow.FieldString, Required: true,
		Default: "BTC/USD", Placeholder: "BTC/USD",
		Help: "Trading pair. BTC/USD, BTC-USD, and BTCUSD all parse.",
	}
}

// resolveProvider looks up a configured exchange by name.
func (p *CryptoPlugin) resolveProvider(name string) (Provider, error) {
	if name == "" {
		name = "coinbase"
	}
	prov, ok := p.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown crypto provider %q", name)
	}
	return prov, nil
}

// quotePayload flattens a quote for a flow message, keeping the whole quote
// under "quote" so nothing is lost.
func quotePayload(q *CryptoQuote) map[string]any {
	return map[string]any{
		"pair":           q.Pair,
		"base":           q.Base,
		"quote_currency": q.Quote,
		"price":          q.Price,
		"change_24h":     q.Change24h,
		"change_pct_24h": q.ChangePct24h,
		"high_24h":       q.High24h,
		"low_24h":        q.Low24h,
		"volume_24h":     q.Volume24h,
		"timestamp":      q.Timestamp,
		"quote":          q,
	}
}

// ---- crypto-price-changed ----

// CryptoPriceChangedNode polls a pair and starts a run when its price moves
// past a threshold, so a flow reacts to the move rather than to the clock.
type CryptoPriceChangedNode struct{ p *CryptoPlugin }

func (CryptoPriceChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "crypto-price-changed",
		Label:             "Crypto Price Changed",
		Category:          flow.CategoryTrigger,
		Folder:            folderCrypto,
		Description:       "Starts a run when a trading pair's price moves past a threshold",
		Icon:              "las la-bitcoin",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			cryptoProviderField(),
			cryptoPairField(),
			{
				ID: "threshold_mode", Label: "Threshold", Type: flow.FieldSelect, Default: "percent",
				Options: []engine.Option{
					{Value: "percent", Label: "Percentage move"},
					{Value: "absolute", Label: "Absolute move"},
					{Value: "any", Label: "Any change"},
				},
			},
			{
				ID: "threshold", Label: "Threshold value", Type: flow.FieldNumber, Default: 1,
				Help: "Percent or price units the pair must move since the last emit. Ignored when any change counts.",
			},
			{
				ID: "interval_s", Label: "Poll interval (seconds)", Type: flow.FieldNumber, Default: 60,
				Help: "How often the price is fetched. Exchanges rate-limit; below 30 seconds risks throttling.",
			},
		},
	}
}

// cryptoNodeConfig is the decoded configuration shared by the crypto nodes.
type cryptoNodeConfig struct {
	Provider      string  `json:"provider"`
	Pair          string  `json:"pair"`
	ThresholdMode string  `json:"threshold_mode"`
	Threshold     float64 `json:"threshold"`
	IntervalS     float64 `json:"interval_s"`
}

// Execute passes the run's seed message through, like every trigger.
func (CryptoPriceChangedNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Start polls until disarmed, emitting only when the move clears the
// threshold. The comparison is against the last emitted price, not the last
// polled one, so a slow drift eventually fires instead of never firing.
func (n *CryptoPriceChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg cryptoNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return err
	}
	pair := normalizePairString(cfg.Pair)
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	if interval <= 0 {
		interval = time.Duration(defaultPollInterval) * time.Second
	}

	var lastEmitted float64
	haveBaseline := false
	for {
		quote, err := prov.FetchQuote(ctx, pair)
		if err != nil {
			// A trigger has no error branch, so a provider hiccup is logged
			// and the next tick tries again.
			ec.Debug(map[string]any{"error": err.Error(), "pair": pair}, "crypto fetch failed")
		} else {
			if moved, delta, pct := priceMoved(lastEmitted, quote.Price, haveBaseline, cfg); moved {
				payload := quotePayload(quote)
				payload["previous_price"] = lastEmitted
				payload["delta"] = delta
				payload["delta_pct"] = pct
				emit(&engine.Msg{
					Topic:   fmt.Sprintf("market:crypto:%s:%s", cfg.Provider, pair),
					Payload: payload,
				})
				lastEmitted = quote.Price
				haveBaseline = true
			}
		}

		select {
		case <-ec.Clock().After(interval):
		case <-ctx.Done():
			return nil
		}
	}
}

// priceMoved reports whether the move since the last emit clears the
// configured threshold, and by how much. The first quote always fires: a flow
// arming mid-session should learn the price rather than wait for a swing.
func priceMoved(previous, current float64, haveBaseline bool, cfg cryptoNodeConfig) (bool, float64, float64) {
	if !haveBaseline {
		return true, 0, 0
	}
	delta := current - previous
	var pct float64
	if previous != 0 {
		pct = delta / previous * 100
	}

	// An unset threshold is the field's default of 1. A threshold of 0 would
	// fire on every poll, which is what "any" is for.
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 1
	}
	switch cfg.ThresholdMode {
	case "absolute":
		return math.Abs(delta) >= threshold, delta, pct
	case "any":
		return delta != 0, delta, pct
	default: // percent
		return math.Abs(pct) >= threshold, delta, pct
	}
}

// ---- crypto-price ----

// CryptoPriceNode is a read-only lookup: no input port, no error branch, one
// fetch per run however many consumers pull it.
type CryptoPriceNode struct{ p *CryptoPlugin }

func (CryptoPriceNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "crypto-price",
		Label:             "Crypto Price",
		Folder:            folderCrypto,
		Description:       "Current price for a trading pair, fetched when a consumer needs it",
		Icon:              "las la-coins",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			cryptoProviderField(),
			cryptoPairField(),
		},
	}
}

func (n *CryptoPriceNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg cryptoNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return nil, err
	}
	pair := normalizePairString(cfg.Pair)
	if pair == "" {
		return nil, fmt.Errorf("a trading pair is required")
	}

	quote, err := prov.FetchQuote(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", pair, err)
	}
	return engine.One("out", &engine.Msg{
		Topic:   fmt.Sprintf("market:crypto:%s:%s", cfg.Provider, pair),
		Payload: quotePayload(quote),
	}), nil
}

// normalizePairString puts a user-entered pair into the canonical
// BASE/QUOTE form the providers expect.
func normalizePairString(pair string) string {
	pair = strings.TrimSpace(pair)
	if pair == "" {
		return ""
	}
	base, quote := parsePair(pair)
	return normalizePair(base, quote)
}
