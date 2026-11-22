package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// staleLimit is how long a calendar's last good events stand in for a failing
// refresh, so a short outage does not cancel a planned firing while a
// deleted event cannot fire forever.
const staleLimit = 24 * time.Hour

// pastDays is how far back a refresh reads, so events in progress and
// moments missed during a short outage are still in the list.
const pastDays = 7

// calendarDriver is one running calendar connection. It holds the last
// events it read and serves them to the plugin's nodes and widgets.
type calendarDriver struct {
	id   uuid.UUID
	name string
	kind string
	src  eventSource
	loc  *time.Location
	days int
	max  int
	deps integrations.Deps
	p    *CalendarPlugin

	// fetchMu serializes refreshes, so concurrent readers share one fetch.
	fetchMu sync.Mutex

	mu        sync.Mutex
	status    integrations.Status
	events    []CalendarEvent
	fetchedAt time.Time
}

// newDriver builds the driver for one calendar connection.
func (p *CalendarPlugin) newDriver(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	src, err := p.sourceFor(model.Kind, model.Name, model.Config)
	if err != nil {
		return nil, err
	}
	var cfg connConfig
	_ = json.Unmarshal(model.Config, &cfg)
	loc, _ := cfg.location()
	days, max := cfg.bounds()
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &calendarDriver{
		id: model.ID, name: model.Name, kind: model.Kind, src: src, loc: loc, days: days, max: max,
		deps: deps, p: p,
		status: integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}, nil
}

func (d *calendarDriver) ID() uuid.UUID { return d.id }
func (d *calendarDriver) Kind() string  { return d.kind }

// Status reports whether the last refresh succeeded.
func (d *calendarDriver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Start registers the driver with the plugin and reads the calendar once, so
// the connection's status says whether it works.
func (d *calendarDriver) Start(ctx context.Context) error {
	d.p.addDriver(d)
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _, _ = d.Events(rctx, time.Now(), 0)
	}()
	return nil
}

// Stop withdraws the driver.
func (d *calendarDriver) Stop() { d.p.removeDriver(d) }

// setStatus records and publishes a state change.
func (d *calendarDriver) setStatus(state string, err error) {
	d.mu.Lock()
	changed := d.status.State != state || (err != nil && d.status.Error != err.Error())
	d.status = integrations.Status{State: state, Since: time.Now()}
	if err != nil {
		d.status.Error = err.Error()
	}
	status := d.status
	d.mu.Unlock()
	if changed && d.deps.PublishStatus != nil {
		d.deps.PublishStatus(status)
	}
}

// Events returns the calendar's occurrences from a week before now through
// the connection's window. A list read within maxAge is reused. When a
// refresh fails, the last good list is returned for up to a day with the time
// it was read, and the connection reports the error.
func (d *calendarDriver) Events(ctx context.Context, now time.Time, maxAge time.Duration) ([]CalendarEvent, time.Time, error) {
	d.fetchMu.Lock()
	defer d.fetchMu.Unlock()

	d.mu.Lock()
	events, fetchedAt := d.events, d.fetchedAt
	d.mu.Unlock()
	if !fetchedAt.IsZero() && maxAge > 0 && now.Sub(fetchedAt) < maxAge {
		return events, time.Time{}, nil
	}

	fresh, err := d.src.fetch(ctx, now.AddDate(0, 0, -pastDays), now.AddDate(0, 0, d.days), d.loc, d.max)
	if err != nil {
		d.setStatus(integrations.StateError, err)
		d.deps.Logger.Debug("calendar refresh failed", zap.String("connection", d.name), zap.Error(err))
		if !fetchedAt.IsZero() && now.Sub(fetchedAt) < staleLimit {
			return events, fetchedAt, nil
		}
		return nil, time.Time{}, fmt.Errorf("reading calendar %q: %w", d.name, err)
	}
	d.mu.Lock()
	d.events, d.fetchedAt = fresh, now
	d.mu.Unlock()
	d.setStatus(integrations.StateConnected, nil)
	return fresh, time.Time{}, nil
}

// addDriver records a running driver so nodes and widget handlers can reach
// it by connection ID.
func (p *CalendarPlugin) addDriver(d *calendarDriver) {
	p.driversMu.Lock()
	defer p.driversMu.Unlock()
	if p.drivers == nil {
		p.drivers = make(map[string]*calendarDriver)
	}
	p.drivers[d.id.String()] = d
}

// removeDriver forgets a stopped driver, unless a newer one replaced it.
func (p *CalendarPlugin) removeDriver(d *calendarDriver) {
	p.driversMu.Lock()
	defer p.driversMu.Unlock()
	if p.drivers[d.id.String()] == d {
		delete(p.drivers, d.id.String())
	}
}

// driverFor returns the running driver of a calendar connection.
func (p *CalendarPlugin) driverFor(id string) (*calendarDriver, error) {
	p.driversMu.Lock()
	defer p.driversMu.Unlock()
	d, ok := p.drivers[id]
	if !ok {
		return nil, fmt.Errorf("calendar connection %s is not running; is it enabled?", id)
	}
	return d, nil
}

// connections lists the running calendar connections.
func (p *CalendarPlugin) connections() []*calendarDriver {
	p.driversMu.Lock()
	defer p.driversMu.Unlock()
	out := make([]*calendarDriver, 0, len(p.drivers))
	for _, d := range p.drivers {
		out = append(out, d)
	}
	return out
}
