package contract

import (
	"bytes"
	"testing"
	"time"

	"oracle/internal/cache"
)

// Cache checks the behavior of a cache.Cache implementation. newCache returns an empty cache that
// shares nothing with the caches of other tests.
func Cache(t *testing.T, newCache func(t *testing.T) cache.Cache) {
	t.Helper()
	for name, check := range map[string]func(t *testing.T, c cache.Cache){
		"a missing key is a miss, not an error":                 missIsNotAnError,
		"keeps any bytes":                                       keepsAnyBytes,
		"Set replaces":                                          setReplaces,
		"keys do not mix":                                       keysDoNotMix,
		"an entry expires after its ttl":                        expires,
		"Flush drops every entry, and the cache works after it": flushDropsEverything,
		"Flush of an empty cache is fine":                       flushOfEmpty,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			check(t, newCache(t))
		})
	}
}

func missIsNotAnError(t *testing.T, c cache.Cache) {
	if v, ok, err := c.Get(t.Context(), "/none?"); ok || err != nil || v != nil {
		t.Errorf("Get = %q, %v, %v; want a clean miss", v, ok, err)
	}
}

func keepsAnyBytes(t *testing.T, c cache.Cache) {
	want := []byte("application/json\n{\"ação\":\"ü\"}\x00\xff\n")
	if err := c.Set(t.Context(), "/tickers?a=1", want, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(t.Context(), "/tickers?a=1")
	if err != nil || !ok || !bytes.Equal(got, want) {
		t.Errorf("Get = %q, %v, %v; want %q", got, ok, err, want)
	}
}

func setReplaces(t *testing.T, c cache.Cache) {
	_ = c.Set(t.Context(), "k", []byte("old"), time.Minute)
	_ = c.Set(t.Context(), "k", []byte("new"), time.Minute)
	if got, _, _ := c.Get(t.Context(), "k"); string(got) != "new" {
		t.Errorf("Get = %q, want new", got)
	}
}

func keysDoNotMix(t *testing.T, c cache.Cache) {
	_ = c.Set(t.Context(), "/a?", []byte("a"), time.Minute)
	if _, ok, _ := c.Get(t.Context(), "/b?"); ok {
		t.Error("a key answered for another")
	}
}

func expires(t *testing.T, c cache.Cache) {
	if err := c.Set(t.Context(), "k", []byte("v"), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(t.Context(), "k"); !ok {
		t.Fatal("entry gone before its ttl")
	}
	time.Sleep(300 * time.Millisecond)
	if _, ok, _ := c.Get(t.Context(), "k"); ok {
		t.Error("entry still there after its ttl")
	}
}

func flushDropsEverything(t *testing.T, c cache.Cache) {
	keys := []string{"/a?", "/b?", "/c?x=1"}
	for _, k := range keys {
		_ = c.Set(t.Context(), k, []byte(k), time.Minute)
	}
	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, ok, _ := c.Get(t.Context(), k); ok {
			t.Errorf("%s survived the flush", k)
		}
	}
	if err := c.Set(t.Context(), "/a?", []byte("fresh"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := c.Get(t.Context(), "/a?"); !ok || string(got) != "fresh" {
		t.Errorf("Get after the flush = %q, %v; want the new entry", got, ok)
	}
}

func flushOfEmpty(t *testing.T, c cache.Cache) {
	if err := c.Flush(t.Context()); err != nil {
		t.Error(err)
	}
}
