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

// folderStocks is where the plugin's nodes sit in the palette.
const folderStocks = "Stocks"

// stockProviderField is the data-source picker the stock nodes share. Only
// Yahoo is guaranteed; the others appear once their API key is configured, so
// a run against an unkeyed provider fails with a clear message rather than
// silently returning nothing.
func stockProviderField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "provider", Label: "Provider", Type: flow.FieldSelect, Default: "yahoo",
		Options: []engine.Option{
			{Value: "yahoo", Label: "Yahoo Finance"},
			{Value: "alphavantage", Label: "Alpha Vantage"},
			{Value: "finnhub", Label: "Finnhub"},
		},
		Help: "Alpha Vantage and Finnhub need their API key set in the plugin's settings.",
	}
}

func tickerField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "symbol", Label: "Symbol", Type: flow.FieldString, Required: true,
		Placeholder: "AAPL",
	}
}

// resolveProvider looks up a configured provider by name.
func (p *StockPlugin) resolveProvider(name string) (Provider, error) {
	if name == "" {
		name = "yahoo"
	}
	prov, ok := p.providers[name]
	if !ok {
		return nil, fmt.Errorf("stock provider %q is not configured; set its API key in the plugin settings", name)
	}
	return prov, nil
}

// stockQuotePayload flattens a quote for a flow message, keeping the whole
// quote under "quote" so nothing is lost.
func stockQuotePayload(q *StockQuote) map[string]any {
	return map[string]any{
		"symbol":         q.Symbol,
		"name":           q.Name,
		"price":          q.Price,
		"change":         q.Change,
		"change_percent": q.ChangePercent,
		"open":           q.Open,
		"high":           q.High,
		"low":            q.Low,
		"previous_close": q.PreviousClose,
		"volume":         q.Volume,
		"currency":       q.Currency,
		"exchange":       q.Exchange,
		"market_state":   q.MarketState,
		"timestamp":      q.Timestamp,
		"quote":          q,
	}
}

// ---- stock-quote-changed ----

// StockQuoteChangedNode polls a ticker and starts a run when its price moves
// past a threshold.
type StockQuoteChangedNode struct{ p *StockPlugin }

func (StockQuoteChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "stock-quote-changed",
		Label:             "Stock Quote Changed",
		Category:          flow.CategoryTrigger,
		Folder:            folderStocks,
		Description:       "Starts a run when a ticker's price moves past a threshold",
		Icon:              "las la-chart-line",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			stockProviderField(),
			tickerField(),
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
				Help: "Percent or price units the ticker must move since the last emit.",
			},
			{
				ID: "interval_s", Label: "Poll interval (seconds)", Type: flow.FieldNumber, Default: 60,
				Help: "How often the quote is fetched.",
			},
			{
				ID: "market_hours_only", Label: "Only while the market is open", Type: flow.FieldBoolean,
				Default: true,
				Help:    "Skips polling outside US market hours, when the quote cannot move.",
			},
		},
	}
}

// stockNodeConfig is the decoded configuration shared by the stock nodes.
type stockNodeConfig struct {
	Provider        string  `json:"provider"`
	Symbol          string  `json:"symbol"`
	ThresholdMode   string  `json:"threshold_mode"`
	Threshold       float64 `json:"threshold"`
	IntervalS       float64 `json:"interval_s"`
	MarketHoursOnly *bool   `json:"market_hours_only"`
}

// marketHoursOnly reads market_hours_only, which defaults to true when the
// graph leaves it out.
func (c stockNodeConfig) marketHoursOnly() bool {
	return c.MarketHoursOnly == nil || *c.MarketHoursOnly
}

// Execute passes the run's seed message through, like every trigger.
func (StockQuoteChangedNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Start polls until disarmed, emitting only when the move clears the
// threshold. Comparison is against the last emitted price, so a slow drift
// eventually fires rather than never firing.
func (n *StockQuoteChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg stockNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return err
	}
	symbol := strings.ToUpper(strings.TrimSpace(cfg.Symbol))
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	if interval <= 0 {
		interval = time.Duration(defaultPollInterval) * time.Second
	}

	var lastEmitted float64
	haveBaseline := false
	for {
		if !cfg.marketHoursOnly() || IsMarketOpen() {
			quote, err := prov.FetchQuote(ctx, symbol)
			if err != nil {
				// A trigger has no error branch; the next tick tries again.
				ec.Debug(map[string]any{"error": err.Error(), "symbol": symbol}, "stock fetch failed")
			} else if moved, delta, pct := quoteMoved(lastEmitted, quote.Price, haveBaseline, cfg); moved {
				payload := stockQuotePayload(quote)
				payload["previous_price"] = lastEmitted
				payload["delta"] = delta
				payload["delta_pct"] = pct
				emit(&engine.Msg{
					Topic:   fmt.Sprintf("market:stock:%s:%s", cfg.Provider, symbol),
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

// quoteMoved reports whether the move since the last emit clears the
// threshold. The first quote always fires, so a flow arming mid-session learns
// the price instead of waiting for a swing.
func quoteMoved(previous, current float64, haveBaseline bool, cfg stockNodeConfig) (bool, float64, float64) {
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

// ---- stock-market-status ----

// StockMarketStatusNode emits on the open/close transition rather than on a
// schedule, so a flow does not have to encode the market calendar itself.
type StockMarketStatusNode struct{ p *StockPlugin }

func (StockMarketStatusNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "stock-market-status",
		Label:             "Market Open/Close",
		Category:          flow.CategoryTrigger,
		Folder:            folderStocks,
		Description:       "Starts a run when the US market opens or closes",
		Icon:              "las la-bell",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "emit_on", Label: "Emit on", Type: flow.FieldSelect, Default: "both",
				Options: []engine.Option{
					{Value: "both", Label: "Open and close"},
					{Value: "open", Label: "Open only"},
					{Value: "close", Label: "Close only"},
				},
			},
			{
				ID: "interval_s", Label: "Check interval (seconds)", Type: flow.FieldNumber, Default: 60,
				Help: "How often the market calendar is checked. This costs nothing: no request is made.",
			},
		},
	}
}

// marketStatusConfig is the decoded stock-market-status configuration.
type marketStatusConfig struct {
	EmitOn    string  `json:"emit_on"`
	IntervalS float64 `json:"interval_s"`
}

// emits reports whether a transition to event starts a run. An emit_on the
// graph leaves out is the catalog default of both.
func (c marketStatusConfig) emits(event string) bool {
	return c.EmitOn == "" || c.EmitOn == "both" || c.EmitOn == event
}

// Execute passes the run's seed message through, like every trigger.
func (StockMarketStatusNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Start watches for the transition, not the state: arming while the market is
// already open does not fire, because nothing happened.
func (StockMarketStatusNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg marketStatusConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	if interval <= 0 {
		interval = time.Minute
	}

	previous := IsMarketOpen()
	for {
		select {
		case <-ec.Clock().After(interval):
		case <-ctx.Done():
			return nil
		}

		current := IsMarketOpen()
		if current == previous {
			continue
		}
		previous = current

		event := "close"
		if current {
			event = "open"
		}
		if !cfg.emits(event) {
			continue
		}
		emit(&engine.Msg{
			Topic: "market:status",
			Payload: map[string]any{
				"event":     event,
				"open":      current,
				"next_open": NextOpenTime().Unix(),
			},
		})
	}
}

// ---- stock-quote ----

// StockQuoteNode is a read-only lookup: no input port, no error branch, one
// fetch per run however many consumers pull it.
type StockQuoteNode struct{ p *StockPlugin }

func (StockQuoteNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "stock-quote",
		Label:             "Stock Quote",
		Folder:            folderStocks,
		Description:       "Current quote for a ticker, fetched when a consumer needs it",
		Icon:              "las la-dollar-sign",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			stockProviderField(),
			tickerField(),
		},
	}
}

func (n *StockQuoteNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg stockNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return nil, err
	}
	symbol := strings.ToUpper(strings.TrimSpace(cfg.Symbol))
	if symbol == "" {
		return nil, fmt.Errorf("a ticker symbol is required")
	}

	quote, err := prov.FetchQuote(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", symbol, err)
	}
	return engine.One("out", &engine.Msg{
		Topic:   fmt.Sprintf("market:stock:%s:%s", cfg.Provider, symbol),
		Payload: stockQuotePayload(quote),
	}), nil
}
