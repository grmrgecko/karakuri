package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// widgetHost is the slice of the host a polling scheduler touches: the stored
// widget sources it reads and the updates it publishes.
type widgetHost struct {
	plugin.HostAPI
	config map[string]string

	mu        sync.Mutex
	published map[string][]CalendarData
}

func (h *widgetHost) GetConfig(_ context.Context, key string) (string, bool, error) {
	v, ok := h.config[key]
	return v, ok, nil
}

func (h *widgetHost) Publish(_ context.Context, topic, _ string, payload []byte) error {
	var data CalendarData
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.published[topic] = append(h.published[topic], data)
	return nil
}

// TestSchedulerPublishesEveryWidgetSource pins that an update pushed to a
// widget carries the events of all its sources. The widget replaces its whole
// list with each update, so an update holding only the source that was just
// polled dropped every other calendar from a widget showing two.
func TestSchedulerPublishesEveryWidgetSource(t *testing.T) {
	start := time.Now().Add(time.Hour)
	host := &widgetHost{config: map[string]string{}, published: map[string][]CalendarData{}}
	p := &CalendarPlugin{host: host}

	var sources []SourceConfig
	for _, title := range []string{"Dentist", "Recital"} {
		src := &listSource{}
		src.set(ev(title, title, start, start.Add(time.Hour)))
		id := uuid.New()
		p.addDriver(&calendarDriver{id: id, name: title, kind: kindICS, src: src, loc: time.UTC, days: 60, max: 5000, p: p,
			deps: integrations.Deps{Logger: zap.NewNop()}})
		sources = append(sources, SourceConfig{ConnectionID: id.String(), Name: title})
	}
	raw, err := json.Marshal(sources)
	require.NoError(t, err, "the widget's sources must encode")
	host.config[sourcesKey("dash", "widget")] = string(raw)

	s := NewScheduler(host, p, gocache.New(time.Minute, time.Minute), time.Hour)
	t.Cleanup(s.Stop)
	for _, src := range sources {
		s.EnsurePolling(src, "dash", "widget")
	}

	topic := "calendar:update:dash:widget"
	require.Eventually(t, func() bool {
		host.mu.Lock()
		defer host.mu.Unlock()
		return len(host.published[topic]) == len(sources)
	}, 3*time.Second, 5*time.Millisecond, "each source's first poll must publish to the widget")

	host.mu.Lock()
	defer host.mu.Unlock()
	for i, data := range host.published[topic] {
		var titles []string
		for _, e := range data.Events {
			titles = append(titles, e.Title)
		}
		assert.ElementsMatchf(t, []string{"Dentist", "Recital"}, titles,
			"update %d must carry both calendars, because the widget shows only what the last update held", i)
	}
}
