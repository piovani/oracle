package cache

import (
	"context"
	"errors"
	"time"

	"oracle/config"

	"github.com/redis/go-redis/v9"
)

// flushBatch is how many keys one UNLINK drops.
const flushBatch = 500

// Redis is the Cache in a Redis server. Every key lives under the configured prefix, so Flush
// leaves alone whatever else the server holds.
type Redis struct {
	client *redis.Client
	prefix string
}

func init() {
	// The client prints a line for every failed connection, once per request while Redis is down;
	// the handler already warns about it, rate limited.
	redis.SetLogger(silent{})
}

type silent struct{}

func (silent) Printf(context.Context, string, ...any) {}

// NewRedis prepares the client; it does not connect until the first call. The timeouts are short
// on purpose: a slow or dead Redis must cost a request milliseconds, not seconds.
func NewRedis(cfg config.RedisConfig) *Redis {
	return &Redis{
		client: redis.NewClient(&redis.Options{
			Addr:         cfg.Addr,
			Password:     cfg.Password,
			DB:           cfg.DB,
			DialTimeout:  300 * time.Millisecond,
			ReadTimeout:  500 * time.Millisecond,
			WriteTimeout: 500 * time.Millisecond,
			MaxRetries:   -1, // fail fast, the caller serves without the cache
		}),
		prefix: cfg.Prefix,
	}
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := r.client.Get(ctx, r.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, r.prefix+key, value, ttl).Err()
}

func (r *Redis) Flush(ctx context.Context) error {
	iter := r.client.Scan(ctx, 0, r.prefix+"*", flushBatch).Iterator()
	batch := make([]string, 0, flushBatch)
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) == flushBatch {
			if err := r.client.Unlink(ctx, batch...).Err(); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}
	return r.client.Unlink(ctx, batch...).Err()
}

// Close releases the connections.
func (r *Redis) Close() error { return r.client.Close() }
