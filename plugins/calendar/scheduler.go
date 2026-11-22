package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"

	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// SourceKey identifies a calendar a widget reads: its connection and the
// calendars it narrows to.
type SourceKey string

// WidgetKey identifies a specific widget instance on a dashboard.
type WidgetKey string

// widgetTarget is one widget a source publishes to.
type widgetTarget struct {
	dashboardID, widgetID string
	topic                 string
}

// SourceEntry tracks the polling goroutine for one source.
type SourceEntry struct {
	key      SourceKey
	source   SourceConfig
	topics   map[WidgetKey]widgetTarget
	stopChan chan struct{}
}

// Scheduler reads the calendars dashboards are showing on an interval and
// publishes changes to the widgets watching them. Polling starts when a
// widget's update topic gains a subscriber and stops when it has none.
type Scheduler struct {
	mu           sync.Mutex
	entries      map[SourceKey]*SourceEntry
	host         plugin.HostAPI
	p            *CalendarPlugin
	cache        *gocache.Cache
	pollInterval time.Duration
}

// NewScheduler creates a scheduler reading through the plugin's connections.
func NewScheduler(host plugin.HostAPI, p *CalendarPlugin, cache *gocache.Cache, pollInterval time.Duration) *Scheduler {
	return &Scheduler{
		entries:      make(map[SourceKey]*SourceEntry),
		host:         host,
		p:            p,
		cache:        cache,
		pollInterval: pollInterval,
	}
}

// EnsurePolling starts or updates polling for a source to publish to the
// given widget's topic.
func (s *Scheduler) EnsurePolling(source SourceConfig, dashboardID, widgetID string) {
	key := sourceKey(source)
	wKey := widgetKey(dashboardID, widgetID)
	target := widgetTarget{
		dashboardID: dashboardID, widgetID: widgetID,
		topic: fmt.Sprintf("calendar:update:%s:%s", dashboardID, widgetID),
	}

	s.mu.Lock()
	entry, exists := s.entries[key]
	if exists {
		entry.topics[wKey] = target
		s.mu.Unlock()
		return
	}
	entry = &SourceEntry{
		key:      key,
		source:   source,
		topics:   map[WidgetKey]widgetTarget{wKey: target},
		stopChan: make(chan struct{}),
	}
	s.entries[key] = entry
	s.mu.Unlock()

	go s.pollLoop(entry)
}

// StopPolling removes a widget's topic from every source; a source with no
// topics left stops polling.
func (s *Scheduler) StopPolling(dashboardID, widgetID string) {
	wKey := widgetKey(dashboardID, widgetID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		delete(entry.topics, wKey)
		if len(entry.topics) == 0 {
			close(entry.stopChan)
			delete(s.entries, key)
		}
	}
}

// Stop signals all polling goroutines to stop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		close(entry.stopChan)
		delete(s.entries, key)
	}
}

func (s *Scheduler) pollLoop(entry *SourceEntry) {
	s.fetchAndPublish(entry)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-entry.stopChan:
			return
		case <-ticker.C:
			s.fetchAndPublish(entry)
		}
	}
}

// fetchAndPublish reads one source and, when it changed since the last read,
// publishes to each of its widgets. A widget replaces its whole event list
// with what an update carries, so the update holds the events of every source
// the widget shows, not this source alone.
func (s *Scheduler) fetchAndPublish(entry *SourceEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := s.p.sourceEvents(ctx, entry.source, s.pollInterval/2)
	if err != nil {
		log.Printf("calendar: reading %s: %v", entry.key, err)
		return
	}

	newJSON, _ := json.Marshal(events)
	newHash := sha256.Sum256(newJSON)
	hashKey := fmt.Sprintf("calendar:hash:%s", entry.key)
	if oldHash, found := s.cache.Get(hashKey); found {
		if oldBytes, ok := oldHash.([]byte); ok && len(oldBytes) == 32 && *(*[32]byte)(oldBytes) == newHash {
			return
		}
	}
	s.cache.Set(hashKey, newHash[:], s.pollInterval*2)

	s.mu.Lock()
	targets := make([]widgetTarget, 0, len(entry.topics))
	for _, t := range entry.topics {
		targets = append(targets, t)
	}
	s.mu.Unlock()

	if s.host == nil {
		return
	}
	for _, target := range targets {
		// The other sources are read through their drivers' caches, so this
		// costs a fetch only for one that has gone unread for half an interval.
		merged := s.p.fetchAllSources(ctx, s.p.loadSources(ctx, target.dashboardID, target.widgetID), s.pollInterval/2)
		if merged == nil {
			merged = []CalendarEvent{}
		}
		payload, _ := json.Marshal(CalendarData{Events: merged, FetchedAt: time.Now().Unix()})
		_ = s.host.Publish(ctx, target.topic, "calendar_update", payload)
	}
}

// sourceKey identifies a source by connection, calendars, colour, and tag,
// since two widgets showing one connection differently publish different
// events.
func sourceKey(src SourceConfig) SourceKey {
	h := sha256.Sum256([]byte(strings.Join([]string{src.ConnectionID, strings.Join(src.Calendars, ","), src.Color, src.Tag}, "|")))
	return SourceKey(fmt.Sprintf("%x", h[:8]))
}

func widgetKey(dashboardID, widgetID string) WidgetKey {
	return WidgetKey(fmt.Sprintf("%s:%s", dashboardID, widgetID))
}
