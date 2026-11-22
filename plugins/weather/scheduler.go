package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/plugin"
	gocache "github.com/patrickmn/go-cache"
)

// LocationKey is the deduplication key for polling: "{provider}:{lat4}:{lon4}".
type LocationKey string

// LocationEntry tracks the polling goroutine state for a single location.
type LocationEntry struct {
	key      LocationKey
	provider string
	lat, lon float64
	stopChan chan struct{}
}

// WatchedLocation is a forecast location being polled for its watchers.
type WatchedLocation struct {
	Provider string
	Lat, Lon float64
}

// Scheduler manages reactive polling goroutines that fetch forecasts when
// WebSocket clients subscribe to weather topics.
type Scheduler struct {
	mu           sync.Mutex
	entries      map[LocationKey]*LocationEntry
	host         plugin.HostAPI
	providers    map[string]Provider
	cache        *gocache.Cache
	pollInterval time.Duration
	language     string
}

// NewScheduler creates a scheduler with the given dependencies.
func NewScheduler(host plugin.HostAPI, providers map[string]Provider, cache *gocache.Cache, pollInterval time.Duration, language string) *Scheduler {
	return &Scheduler{
		entries:      make(map[LocationKey]*LocationEntry),
		host:         host,
		providers:    providers,
		cache:        cache,
		pollInterval: pollInterval,
		language:     language,
	}
}

// EnsurePolling starts a polling goroutine for the given location if one is not
// already running. Lat/lon are rounded to 4 decimal places for deduplication.
func (s *Scheduler) EnsurePolling(providerName string, lat, lon float64) {
	key := locationKey(providerName, lat, lon)

	s.mu.Lock()
	if _, exists := s.entries[key]; exists {
		s.mu.Unlock()
		return
	}

	entry := &LocationEntry{
		key:      key,
		provider: providerName,
		lat:      roundCoord(lat),
		lon:      roundCoord(lon),
		stopChan: make(chan struct{}),
	}
	s.entries[key] = entry
	s.mu.Unlock()

	go s.pollLoop(entry, providerName, roundCoord(lat), roundCoord(lon))
}

// StopPolling signals the goroutine for the given location to stop.
func (s *Scheduler) StopPolling(providerName string, lat, lon float64) {
	key := locationKey(providerName, lat, lon)

	s.mu.Lock()
	entry, exists := s.entries[key]
	if exists {
		close(entry.stopChan)
		delete(s.entries, key)
	}
	s.mu.Unlock()
}

// Watched lists the locations currently being polled.
func (s *Scheduler) Watched() []WatchedLocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WatchedLocation, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, WatchedLocation{Provider: entry.provider, Lat: entry.lat, Lon: entry.lon})
	}
	return out
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

func (s *Scheduler) pollLoop(entry *LocationEntry, providerName string, lat, lon float64) {
	cacheKey := fmt.Sprintf("forecast:%s", entry.key)
	topic := fmt.Sprintf("weather:forecast:%s:%s:%s", providerName, formatCoord(lat), formatCoord(lon))

	// Perform an initial fetch immediately.
	s.fetchAndPublish(providerName, cacheKey, topic, lat, lon)

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-entry.stopChan:
			return
		case <-ticker.C:
			s.fetchAndPublish(providerName, cacheKey, topic, lat, lon)
		}
	}
}

func (s *Scheduler) fetchAndPublish(providerName, cacheKey, topic string, lat, lon float64) {
	prov, ok := s.providers[providerName]
	if !ok {
		log.Printf("weather: unknown provider %q for %s", providerName, cacheKey)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := prov.FetchForecast(ctx, lat, lon, s.language)
	if err != nil {
		log.Printf("weather: fetch error for %s: %v", cacheKey, err)
		return
	}

	// Compare with the previous data hash to avoid publishing unchanged forecasts.
	newJSON, _ := json.Marshal(data)
	newHash := sha256.Sum256(newJSON)
	hashKey := cacheKey + ":hash"

	if oldHash, found := s.cache.Get(hashKey); found {
		if oldBytes, ok := oldHash.([]byte); ok && len(oldBytes) == 32 {
			if *(*[32]byte)(oldBytes) == newHash {
				return
			}
		}
	}

	// Store in cache with TTL of 2x poll interval.
	ttl := s.pollInterval * 2
	s.cache.Set(cacheKey, data, ttl)
	s.cache.Set(hashKey, newHash[:], ttl)

	// Publish update to MQ.
	if s.host != nil {
		_ = s.host.Publish(ctx, topic, "weather_update", newJSON)
	}
}

func locationKey(provider string, lat, lon float64) LocationKey {
	return LocationKey(fmt.Sprintf("%s:%s:%s", provider, formatCoord(roundCoord(lat)), formatCoord(roundCoord(lon))))
}

func roundCoord(v float64) float64 {
	// Match the browser's Math.round, including negative coordinates and ties.
	return math.Floor(v*10000+0.5) / 10000
}

func formatCoord(v float64) string {
	return fmt.Sprintf("%.4f", v)
}
