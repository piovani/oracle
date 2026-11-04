// Package cache keeps the answers of the heavy API routes for a short time, in a Redis shared by
// every instance of the API and by the sync command, which empties it when new quotes arrive.
package cache

import (
	"context"
	"time"
)

// Cache is a key-value store with expiry. A miss is not an error: Get reports false.
// Implementations are safe for concurrent use.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set keeps value for ttl, replacing what the key held.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Flush drops everything this cache holds and nothing else: a new, empty cache.
	Flush(ctx context.Context) error
}
