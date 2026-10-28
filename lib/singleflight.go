package lib

import (
	"sync"
	"time"
)

// FlightCache dedupes concurrent loads per key and caches results with a per-key TTL.
// Concurrent callers share one in-flight call; on refresh failure the last cached
// value is served (stale fallback) when available.
type FlightCache struct {
	mu    sync.Mutex
	flies map[string]*flight
	cache map[string]cacheEntry
}

type flight struct {
	done chan struct{}
	val  any
	err  error
}

type cacheEntry struct {
	val     any
	expires time.Time
}

// NewFlightCache creates an empty FlightCache.
func NewFlightCache() *FlightCache {
	return &FlightCache{
		flies: make(map[string]*flight),
		cache: make(map[string]cacheEntry),
	}
}

// Do returns the cached value while fresh (ttl > 0); otherwise it runs fn at most
// once per key — concurrent callers for the same key share the same result.
// ttl == 0 means dedupe only: never cache, just collapse concurrent calls.
func (fc *FlightCache) Do(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	now := time.Now()

	fc.mu.Lock()
	if e, ok := fc.cache[key]; ok && now.Before(e.expires) {
		fc.mu.Unlock()
		return e.val, nil
	}
	if f, ok := fc.flies[key]; ok {
		fc.mu.Unlock()
		<-f.done
		return fc.result(key, f)
	}
	f := &flight{done: make(chan struct{})}
	fc.flies[key] = f
	fc.mu.Unlock()

	f.val, f.err = fn()

	fc.mu.Lock()
	if f.err == nil && ttl > 0 {
		fc.cache[key] = cacheEntry{val: f.val, expires: time.Now().Add(ttl)}
	}
	delete(fc.flies, key)
	fc.mu.Unlock()
	close(f.done)

	return fc.result(key, f)
}

func (fc *FlightCache) result(key string, f *flight) (any, error) {
	if f.err == nil {
		return f.val, nil
	}
	// Refresh failed — serve the stale value instead of the error when possible.
	// Warning: callers cannot distinguish stale from fresh data here.
	fc.mu.Lock()
	e, ok := fc.cache[key]
	fc.mu.Unlock()
	if ok {
		return e.val, nil
	}
	return nil, f.err
}
