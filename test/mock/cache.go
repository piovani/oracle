package mock

import (
	"context"
	"sync"
	"time"
)

// Cache is an in-memory cache.Cache. It honors the contract, expiry included, and is safe for
// concurrent use, like Redis: the API calls it from many goroutines.
type Cache struct {
	GetErr, SetErr, FlushErr error

	mu                  sync.Mutex
	entries             map[string]cacheEntry
	gets, sets, flushes int
}

type cacheEntry struct {
	value   []byte
	expires time.Time
}

func (c *Cache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	if c.GetErr != nil {
		return nil, false, c.GetErr
	}
	e, ok := c.entries[key]
	if !ok || !time.Now().Before(e.expires) {
		return nil, false, nil
	}
	return e.value, true, nil
}

func (c *Cache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sets++
	if c.SetErr != nil {
		return c.SetErr
	}
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	c.entries[key] = cacheEntry{value: append([]byte(nil), value...), expires: time.Now().Add(ttl)}
	return nil
}

func (c *Cache) Flush(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushes++
	if c.FlushErr != nil {
		return c.FlushErr
	}
	clear(c.entries)
	return nil
}

// GetCalls, SetCalls and FlushCalls count the calls, to check that a hit did not recompute and a sync emptied the cache.
func (c *Cache) GetCalls() int   { c.mu.Lock(); defer c.mu.Unlock(); return c.gets }
func (c *Cache) SetCalls() int   { c.mu.Lock(); defer c.mu.Unlock(); return c.sets }
func (c *Cache) FlushCalls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.flushes }

// Keys lists what the cache holds that has not expired, for tests.
func (c *Cache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var keys []string
	for k, e := range c.entries {
		if time.Now().Before(e.expires) {
			keys = append(keys, k)
		}
	}
	return keys
}
