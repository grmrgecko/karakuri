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

const (
	// historySize is the number of recent data points kept in the circular buffer.
	historySize = 24
	// broadcastMinInterval is the minimum time between broadcasts for the same pair.
	broadcastMinInterval = time.Second
)

// PairKey is the deduplication key for polling: "{provider}:{BASE/QUOTE}".
type PairKey string

// PairEntry tracks the polling goroutine state for a single trading pair.
type PairEntry struct {
	key      PairKey
	stopChan chan struct{}
}

// Scheduler manages reactive polling goroutines that fetch crypto quotes when
// WebSocket clients subscribe to crypto topics. It maintains a global registry
// of active pairs to prevent duplicate API calls.
type Scheduler struct {
	mu            sync.Mutex
	entries       map[PairKey]*PairEntry
	host          plugin.HostAPI
	providers     map[string]Provider
	cache         *gocache.Cache
	pollInterval  time.Duration
	history       map[PairKey][]CryptoQuote
	lastBroadcast map[PairKey]time.Time
}

// NewScheduler creates a scheduler with the given dependencies.
func NewScheduler(host plugin.HostAPI, providers map[string]Provider, cache *gocache.Cache, pollInterval time.Duration) *Scheduler {
	return &Scheduler{
		entries:       make(map[PairKey]*PairEntry),
		host:          host,
		providers:     providers,
		cache:         cache,
		pollInterval:  pollInterval,
		history:       make(map[PairKey][]CryptoQuote),
		lastBroadcast: make(map[PairKey]time.Time),
	}
}

// EnsurePolling starts a polling goroutine for the given pair if one is not already running.
func (s *Scheduler) EnsurePolling(providerName, pair string) {
	key := pairKey(providerName, pair)

	s.mu.Lock()
	if _, exists := s.entries[key]; exists {
		s.mu.Unlock()
		return
	}

	entry := &PairEntry{
		key:      key,
		stopChan: make(chan struct{}),
	}
	s.entries[key] = entry
	s.mu.Unlock()

	go s.pollLoop(entry, providerName, strings.ToUpper(pair))
}

// StopPolling signals the goroutine for the given pair to stop.
func (s *Scheduler) StopPolling(providerName, pair string) {
	key := pairKey(providerName, pair)

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

// GetHistory returns the circular buffer of recent quotes for a pair.
func (s *Scheduler) GetHistory(providerName, pair string) []CryptoQuote {
	key := pairKey(providerName, pair)

	s.mu.Lock()
	defer s.mu.Unlock()

	if hist, ok := s.history[key]; ok {
		result := make([]CryptoQuote, len(hist))
		copy(result, hist)
		return result
	}
	return nil
}

func (s *Scheduler) pollLoop(entry *PairEntry, providerName, pair string) {
	cacheKey := fmt.Sprintf("quote:%s", entry.key)
	topic := fmt.Sprintf("market:crypto:%s:%s", providerName, pair)

	// Perform an initial fetch immediately.
	s.fetchAndPublish(entry.key, cacheKey, topic, providerName, pair)

	pollTicker := time.NewTicker(s.pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-entry.stopChan:
			return
		case <-pollTicker.C:
			s.fetchAndPublish(entry.key, cacheKey, topic, providerName, pair)
		}
	}
}

func (s *Scheduler) fetchAndPublish(key PairKey, cacheKey, topic, providerName, pair string) {
	prov, ok := s.providers[providerName]
	if !ok {
		log.Printf("crypto: unknown provider %q for %s", providerName, cacheKey)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := prov.FetchQuote(ctx, pair)
	if err != nil {
		log.Printf("crypto: fetch error for %s: %v", cacheKey, err)
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

	// Append to circular history buffer.
	s.mu.Lock()
	hist := s.history[key]
	hist = append(hist, *data)
	if len(hist) > historySize {
		hist = hist[len(hist)-historySize:]
	}
	s.history[key] = hist

	// Throttle broadcasts to 1 per second max.
	now := time.Now()
	lastTime := s.lastBroadcast[key]
	canBroadcast := now.Sub(lastTime) >= broadcastMinInterval
	if canBroadcast {
		s.lastBroadcast[key] = now
	}
	s.mu.Unlock()

	// Publish update to MQ if not throttled.
	if canBroadcast && s.host != nil {
		_ = s.host.Publish(ctx, topic, "crypto_update", newJSON)
	}
}

func pairKey(provider, pair string) PairKey {
	return PairKey(fmt.Sprintf("%s:%s", provider, strings.ToUpper(pair)))
}
