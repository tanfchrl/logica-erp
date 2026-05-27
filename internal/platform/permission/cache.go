package permission

import (
	"sync"
	"time"
)

// Tiny TTL cache. Concurrent-safe. Intentionally simple; can be swapped for ristretto later.
type cache struct {
	ttl  time.Duration
	mu   sync.RWMutex
	data map[string]cacheEntry
}

type cacheEntry struct {
	v       any
	expires time.Time
}

func newCache(ttl time.Duration) *cache {
	return &cache{ttl: ttl, data: map[string]cacheEntry{}}
}

func (c *cache) get(k string) (any, bool) {
	c.mu.RLock()
	e, ok := c.data[k]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		c.mu.Lock()
		delete(c.data, k)
		c.mu.Unlock()
		return nil, false
	}
	return e.v, true
}

func (c *cache) set(k string, v any) {
	c.mu.Lock()
	c.data[k] = cacheEntry{v: v, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *cache) clear() {
	c.mu.Lock()
	c.data = map[string]cacheEntry{}
	c.mu.Unlock()
}
