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

	"github.com/grmrgecko/karakuri/pkg/plugin"
	gocache "github.com/patrickmn/go-cache"
)

// TickerKey is the deduplication key for polling: "{provider}:{SYMBOL}".
type TickerKey string

// TickerEntry tracks the polling goroutine state for a single ticker.
type TickerEntry struct {
	key      TickerKey
	stopChan chan struct{}
}

// Scheduler manages reactive polling goroutines that fetch stock quotes when
// WebSocket clients subscribe to stock topics.
type Scheduler struct {
	mu              sync.Mutex
	entries         map[TickerKey]*TickerEntry
	host            plugin.HostAPI
	providers       map[string]Provider
	cache           *gocache.Cache
	pollInterval    time.Duration
	marketHoursOnly bool
}

// NewScheduler creates a scheduler with the given dependencies.
func NewScheduler(host plugin.HostAPI, providers map[string]Provider, cache *gocache.Cache, pollInterval time.Duration, marketHoursOnly bool) *Scheduler {
	return &Scheduler{
		entries:         make(map[TickerKey]*TickerEntry),
		host:            host,
		providers:       providers,
		cache:           cache,
		pollInterval:    pollInterval,
		marketHoursOnly: marketHoursOnly,
	}
}

// EnsurePolling starts a polling goroutine for the given ticker if one is not already running.
func (s *Scheduler) EnsurePolling(providerName, ticker string) {
	key := tickerKey(providerName, ticker)

	s.mu.Lock()
	if _, exists := s.entries[key]; exists {
		s.mu.Unlock()
		return
	}

	entry := &TickerEntry{
		key:      key,
		stopChan: make(chan struct{}),
	}
	s.entries[key] = entry
	s.mu.Unlock()

	go s.pollLoop(entry, providerName, strings.ToUpper(ticker))
}

// StopPolling signals the goroutine for the given ticker to stop.
func (s *Scheduler) StopPolling(providerName, ticker string) {
	key := tickerKey(providerName, ticker)

	s.mu.Lock()
	entry, exists := s.entries[key]
	if exists {
		close(entry.stopChan)
		delete(s.entries, key)
	}
	s.mu.Unlock()
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

func (s *Scheduler) pollLoop(entry *TickerEntry, providerName, ticker string) {
	cacheKey := fmt.Sprintf("quote:%s", entry.key)
	topic := fmt.Sprintf("market:stock:%s:%s", providerName, ticker)

	// Perform an initial fetch immediately.
	s.fetchAndPublish(cacheKey, topic, providerName, ticker)

	pollTicker := time.NewTicker(s.pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-entry.stopChan:
			return
		case <-pollTicker.C:
			if s.marketHoursOnly && !IsMarketOpen() {
				nextOpen := NextOpenTime()
				sleepDur := time.Until(nextOpen)
				if sleepDur > 0 {
					select {
					case <-entry.stopChan:
						return
					case <-time.After(sleepDur):
					}
				}
				continue
			}
			s.fetchAndPublish(cacheKey, topic, providerName, ticker)
		}
	}
}

func (s *Scheduler) fetchAndPublish(cacheKey, topic, providerName, ticker string) {
	prov, ok := s.providers[providerName]
	if !ok {
		log.Printf("stock: unknown provider %q for %s", providerName, cacheKey)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := prov.FetchQuote(ctx, ticker)
	if err != nil {
		log.Printf("stock: fetch error for %s: %v", cacheKey, err)
		return
	}

	// Compare with the previous data hash to avoid publishing unchanged quotes.
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
		_ = s.host.Publish(ctx, topic, "stock_update", newJSON)
	}
}

func tickerKey(provider, ticker string) TickerKey {
	return TickerKey(fmt.Sprintf("%s:%s", provider, strings.ToUpper(ticker)))
}
