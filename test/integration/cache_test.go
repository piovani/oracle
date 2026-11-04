//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"oracle/config"
	"oracle/internal/cache"
	"oracle/test/contract"
	"oracle/test/fixture"

	"github.com/redis/go-redis/v9"
)

// TestRedisCacheContract runs against Redis the suite the in-memory mock also passes.
func TestRedisCacheContract(t *testing.T) {
	t.Parallel()
	contract.Cache(t, func(t *testing.T) cache.Cache {
		t.Helper()
		c := cache.NewRedis(redisFor(uniqueName("contract_")))
		t.Cleanup(func() { _ = c.Close() })
		return c
	})
}

// Flush must only drop its own keys: the server can be shared with other things, and with the
// caches of other environments.
func TestRedisFlushKeepsOtherPrefixes(t *testing.T) {
	t.Parallel()
	mine := cache.NewRedis(redisFor(uniqueName("mine_")))
	other := cache.NewRedis(redisFor(uniqueName("other_")))
	t.Cleanup(func() { _ = mine.Close(); _ = other.Close() })
	_ = mine.Set(t.Context(), "/a?", []byte("a"), time.Minute)
	_ = other.Set(t.Context(), "/a?", []byte("b"), time.Minute)

	if err := mine.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := mine.Get(t.Context(), "/a?"); ok {
		t.Error("own key survived the flush")
	}
	if got, ok, _ := other.Get(t.Context(), "/a?"); !ok || string(got) != "b" {
		t.Errorf("the key of another prefix = %q, %v; want it untouched", got, ok)
	}
}

// More keys than one UNLINK batch, so the batching loop runs.
func TestRedisFlushDropsManyKeys(t *testing.T) {
	t.Parallel()
	name := uniqueName("many_")
	prefix := redisFor(name).Prefix
	c := cache.NewRedis(redisFor(name))
	t.Cleanup(func() { _ = c.Close() })
	const keys = 1300
	for i := range keys {
		if err := c.Set(t.Context(), fmt.Sprintf("/k%d?", i), []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if left := keysUnder(t, prefix); left != 0 {
		t.Errorf("%d keys left after the flush", left)
	}
}

// keysUnder counts the keys of a prefix straight on the server, without going through the cache under test.
func keysUnder(t *testing.T, prefix string) int {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = client.Close() })
	n := 0
	for iter := client.Scan(t.Context(), 0, prefix+"*", 500).Iterator(); iter.Next(t.Context()); {
		n++
	}
	return n
}

func TestRedisDownIsAnErrorNotAHang(t *testing.T) {
	t.Parallel()
	c := cache.NewRedis(config.RedisConfig{Addr: "127.0.0.1:1", Prefix: "x:"})
	t.Cleanup(func() { _ = c.Close() })

	start := time.Now()
	_, _, getErr := c.Get(t.Context(), "k")
	setErr := c.Set(t.Context(), "k", []byte("v"), time.Minute)
	flushErr := c.Flush(t.Context())

	if getErr == nil || setErr == nil || flushErr == nil {
		t.Errorf("errors = %v, %v, %v; want all three", getErr, setErr, flushErr)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("three calls to a dead Redis took %s", took)
	}
}

func TestCLISyncEmptiesTheCache(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)
	c := cache.NewRedis(redisFor(cfg.Name))
	t.Cleanup(func() { _ = c.Close() })
	_ = c.Set(t.Context(), "/tickers?", []byte("application/json\n[]"), time.Minute)

	out := oracle(t, cfg, nil, "sync", "-since", "2999")

	if out.code != 0 || !strings.Contains(out.stderr, "cache emptied") {
		t.Fatalf("sync:\n%s", out)
	}
	if _, ok, _ := c.Get(t.Context(), "/tickers?"); ok {
		t.Error("the cached answer survived the sync")
	}
}

func TestCLISyncWithRedisDownIsOnlyAWarning(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)

	out := oracle(t, cfg, []string{"REDIS_ADDR=127.0.0.1:1"}, "sync", "-since", "2999")

	if out.code != 0 || !strings.Contains(out.stderr, "cache not emptied") || !strings.Contains(out.stderr, "quote history is up to date") {
		t.Errorf("sync with Redis down:\n%s", out)
	}
}

func cacheState(t *testing.T, url string) (state, body string) {
	t.Helper()
	resp := get(t, url)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, b)
	}
	return resp.Header.Get("X-Cache"), string(b)
}

// The whole life of an answer: computed once, replayed, dropped by the sync, computed again.
func TestCLIAPIAnswersFromTheCacheUntilASyncEmptiesIt(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	// WIN3 ends the day before NEW3, so storing NEW3 is a new session: the API also keeps the ticker list in memory until the latest session changes.
	insert(t, conn, fixture.Trend("WIN3", 300, fixture.Today().AddDate(0, 0, -1), nil))
	base := startAPI(t, cfg)

	first, body := cacheState(t, base+"/tickers")
	second, again := cacheState(t, base+"/tickers")
	if first != "MISS" || second != "HIT" || body != again {
		t.Fatalf("X-Cache %q then %q (same body %v), want MISS then HIT", first, second, body == again)
	}

	// A new session arrives. The cache still answers until the sync empties it...
	insert(t, conn, fixture.Trend("NEW3", 300, fixture.Today(), nil))
	if state, stale := cacheState(t, base+"/tickers"); state != "HIT" || stale != body {
		t.Fatalf("before the sync: X-Cache %q, want the cached HIT", state)
	}

	out := oracle(t, cfg, nil, "sync", "-since", "2999")
	if out.code != 0 {
		t.Fatalf("sync:\n%s", out)
	}

	state, fresh := cacheState(t, base+"/tickers")
	if state != "MISS" || !strings.Contains(fresh, "NEW3") {
		t.Errorf("after the sync: X-Cache %q, NEW3 listed %v; want a MISS with the new data", state, strings.Contains(fresh, "NEW3"))
	}
}

func TestCLIAPIWorksWithRedisDown(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	insert(t, conn, fixture.Trend("WIN3", 300, fixture.Today(), nil))
	base := startAPI(t, cfg, "REDIS_ADDR=127.0.0.1:1")

	for range 2 {
		if state, body := cacheState(t, base+"/tickers"); state != "MISS" || !strings.Contains(body, "WIN3") {
			t.Errorf("X-Cache %q body %s; want the plain answer, uncached", state, body)
		}
	}
}
