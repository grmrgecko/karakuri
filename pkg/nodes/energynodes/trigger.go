package energynodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Durable store keys and limits of the period trigger.
const (
	firedKey     = "fired"
	seenUntilKey = "seen_until"
	// catchUpLimit bounds how far back boundaries missed while the server
	// was down are looked for.
	catchUpLimit = 7 * 24 * time.Hour
	// recheck bounds a sleep, so tariff edits are seen within a minute.
	recheck = time.Minute
)

// PricePeriodTrigger starts a run when the tariff's price period changes.
type PricePeriodTrigger struct{}

func (PricePeriodTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "energy-price-period",
		Label:             "Energy Price Period",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderEnergy,
		Description:       "Starts a run when the electricity price period changes, such as peak starting",
		Icon:              "las la-clock",
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Period", Type: flow.PortObject}},
		ConfigSpec: append([]engine.FieldSpec{
			{ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON, Help: "Payload a test run starts with."},
		}, engine.MissedDeadlineFields()...),
	}
}

type periodConfig struct {
	MissedDeadline string `json:"missed_deadline"`
	MissedGrace    string `json:"missed_grace"`
}

// Execute passes the seeded message through, or the period now on a test run.
func (PricePeriodTrigger) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	ts, err := loadTariffs()
	if err != nil {
		return nil, err
	}
	now := ec.Clock().Now()
	p, err := ts.PriceAt(now, energy.Location(), nil, true)
	if err != nil {
		return nil, err
	}
	return engine.One("out", boundaryMsg(ts, now, p, "", now)), nil
}

func loadTariffs() (energy.Tariffs, error) {
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, err
	}
	return energy.SortTariffs(list), nil
}

// boundaryMsg builds the run's message for one boundary.
func boundaryMsg(ts energy.Tariffs, at time.Time, p energy.Price, previous string, now time.Time) *engine.Msg {
	payload := map[string]any{"period": p.Period, "rate": p.Import, "currency": p.Currency,
		"starts_at": at.Format(time.RFC3339), "previous_period": previous}
	if next, _, err := ts.NextChange(at, energy.Location()); err == nil {
		payload["ends_at"] = next.Format(time.RFC3339)
	}
	if p.Export != nil {
		payload["export_rate"] = *p.Export
	}
	return &engine.Msg{Topic: "energy:period", Payload: payload, Meta: map[string]any{"fired_at": now.Format(time.RFC3339)}}
}

// Start fires at each price boundary. Fired boundaries are recorded in the
// durable store before the run starts, so a restart fires none twice, and
// boundaries passed while the server was down are caught up from the stored
// watermark, up to a week back, following the missed-deadline setting.
func (PricePeriodTrigger) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg periodConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	grace := 30 * time.Second
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.MissedGrace)); err == nil && d >= 0 {
		grace = d
	}
	drop := cfg.MissedDeadline == "drop"
	store := ec.DurableStore()
	armedAt := ec.Clock().Now()
	from := armedAt
	if raw, ok := store.Get(seenUntilKey); ok {
		if s, _ := raw.(string); s != "" {
			if seen, err := time.Parse(time.RFC3339Nano, s); err == nil && seen.Before(armedAt) {
				from = seen
				if earliest := armedAt.Add(-catchUpLimit); from.Before(earliest) {
					from = earliest
				}
			}
		}
	}
	store.Set(seenUntilKey, armedAt.Format(time.RFC3339Nano))
	loc := energy.Location()

	for {
		now := ec.Clock().Now()
		wait := recheck
		ts, err := loadTariffs()
		// A failed read keeps the cursor so its boundaries are caught up. A read
		// that finds no tariff has no boundaries, so the cursor moves on, or a
		// tariff saved later would fire everything since arming.
		if err == nil {
			cursor := from
			for len(ts) > 0 {
				at, p, err := ts.NextChange(cursor, loc)
				if err != nil || at.After(now) {
					if err == nil && at.Sub(now) < wait {
						wait = at.Sub(now)
					}
					break
				}
				prev, _ := ts.PriceAt(at.Add(-time.Minute), loc, nil, true)
				cursor = at
				if !claim(store, at) {
					continue
				}
				msg := boundaryMsg(ts, at, p, prev.Period, now)
				late := now.Sub(at)
				if at.Before(armedAt) && late > grace {
					if drop {
						if rec, ok := ec.(engine.TriggerRecorder); ok {
							rec.Skipped(msg, fmt.Sprintf("missed the %q period starting %s by %s; dropped",
								p.Period, at.Format(time.RFC3339), late.Round(time.Second)))
						}
						continue
					}
					msg.Meta["late_ms"] = late.Milliseconds()
				}
				emit(msg)
			}
			from = now
			store.Set(seenUntilKey, now.Format(time.RFC3339Nano))
			prune(store, now.Add(-catchUpLimit))
		}
		if wait < time.Second {
			wait = time.Second
		}
		select {
		case <-ec.Clock().After(wait):
		case <-ctx.Done():
			return nil
		}
	}
}

// claim records a boundary as fired, reporting false when it already was.
func claim(store engine.NodeStore, at time.Time) bool {
	id := at.UTC().Format(time.RFC3339)
	claimed := false
	store.Update(firedKey, func(cur any, _ bool) any {
		fired, _ := cur.(map[string]any)
		if fired == nil {
			fired = map[string]any{}
		}
		if _, done := fired[id]; done {
			return fired
		}
		fired[id] = true
		claimed = true
		return fired
	})
	return claimed
}

// prune forgets boundaries before cutoff.
func prune(store engine.NodeStore, cutoff time.Time) {
	store.Update(firedKey, func(cur any, ok bool) any {
		fired, _ := cur.(map[string]any)
		if !ok || fired == nil {
			return cur
		}
		for id := range fired {
			if t, err := time.Parse(time.RFC3339, id); err != nil || t.Before(cutoff) {
				delete(fired, id)
			}
		}
		return fired
	})
}
