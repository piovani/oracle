package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oracle/internal/handler"
	"oracle/test/mock"
)

// counting answers {"n": <call number>}, so a repeated body means the answer came from the cache.
func counting(calls *atomic.Int32, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"n":` + strconv.Itoa(int(n)) + `}`))
	}
}

func call(t *testing.T, h http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody))
	return rec
}

func TestResponseCacheServesTheSecondCallFromTheCache(t *testing.T) {
	var calls atomic.Int32
	h := handler.NewResponseCache(&mock.Cache{}, time.Minute).Wrap(counting(&calls, http.StatusOK))

	first := call(t, h, http.MethodGet, "/signals?tier=forte")
	second := call(t, h, http.MethodGet, "/signals?tier=forte")

	if calls.Load() != 1 {
		t.Errorf("handler ran %d times, want once", calls.Load())
	}
	if first.Header().Get("X-Cache") != "MISS" || second.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q then %q, want MISS then HIT", first.Header().Get("X-Cache"), second.Header().Get("X-Cache"))
	}
	if first.Body.String() != second.Body.String() || second.Code != http.StatusOK || second.Header().Get("Content-Type") != "application/json" {
		t.Errorf("hit differs from the original: %d %q %q", second.Code, second.Header().Get("Content-Type"), second.Body)
	}
}

func TestResponseCacheKeysByPathAndQuery(t *testing.T) {
	var calls atomic.Int32
	h := handler.NewResponseCache(&mock.Cache{}, time.Minute).Wrap(counting(&calls, http.StatusOK))

	call(t, h, http.MethodGet, "/signals?a=1&b=2")
	same := call(t, h, http.MethodGet, "/signals?b=2&a=1") // the order of the parameters does not matter
	call(t, h, http.MethodGet, "/signals?a=1")
	call(t, h, http.MethodGet, "/signals")
	call(t, h, http.MethodGet, "/recommendations?a=1&b=2")

	if same.Header().Get("X-Cache") != "HIT" {
		t.Errorf("same parameters in another order: X-Cache = %q, want HIT", same.Header().Get("X-Cache"))
	}
	if calls.Load() != 4 {
		t.Errorf("handler ran %d times, want 4 (four different keys)", calls.Load())
	}
}

func TestResponseCacheKeepsOnlySuccessfulGets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		status int
	}{
		{"not found", http.MethodGet, http.StatusNotFound},
		{"server error", http.MethodGet, http.StatusInternalServerError},
		{"timeout", http.MethodGet, http.StatusServiceUnavailable},
		{"another method", http.MethodPost, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := &mock.Cache{}
			h := handler.NewResponseCache(c, time.Minute).Wrap(counting(&calls, tc.status))

			call(t, h, tc.method, "/x")
			call(t, h, tc.method, "/x")

			if calls.Load() != 2 || len(c.Keys()) != 0 {
				t.Errorf("handler ran %d times, cache holds %v; want 2 and nothing", calls.Load(), c.Keys())
			}
		})
	}
}

func TestResponseCacheExpires(t *testing.T) {
	var calls atomic.Int32
	h := handler.NewResponseCache(&mock.Cache{}, 20*time.Millisecond).Wrap(counting(&calls, http.StatusOK))

	call(t, h, http.MethodGet, "/x")
	time.Sleep(60 * time.Millisecond)
	again := call(t, h, http.MethodGet, "/x")

	if calls.Load() != 2 || again.Header().Get("X-Cache") != "MISS" {
		t.Errorf("after the ttl: handler ran %d times, X-Cache %q; want 2 and MISS", calls.Load(), again.Header().Get("X-Cache"))
	}
}

func TestResponseCacheServesWithoutAFailingCache(t *testing.T) {
	var calls atomic.Int32
	c := &mock.Cache{GetErr: errors.New("redis down"), SetErr: errors.New("redis down")}
	h := handler.NewResponseCache(c, time.Minute).Wrap(counting(&calls, http.StatusOK))

	for range 2 {
		if rec := call(t, h, http.MethodGet, "/x"); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), `{"n":`) {
			t.Errorf("got %d %q, want the handler's own answer", rec.Code, rec.Body)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("handler ran %d times, want 2", calls.Load())
	}
}

func TestResponseCacheIgnoresACorruptEntry(t *testing.T) {
	var calls atomic.Int32
	c := &mock.Cache{}
	_ = c.Set(t.Context(), "/x?", []byte("no newline here"), time.Minute)
	h := handler.NewResponseCache(c, time.Minute).Wrap(counting(&calls, http.StatusOK))

	rec := call(t, h, http.MethodGet, "/x")

	if calls.Load() != 1 || rec.Header().Get("X-Cache") != "MISS" {
		t.Errorf("handler ran %d times, X-Cache %q; want 1 and MISS", calls.Load(), rec.Header().Get("X-Cache"))
	}
}

func TestResponseCacheSkipsHugeAnswers(t *testing.T) {
	var calls atomic.Int32
	huge := func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(make([]byte, 5<<20))
	}
	c := &mock.Cache{}
	h := handler.NewResponseCache(c, time.Minute).Wrap(huge)

	call(t, h, http.MethodGet, "/x")
	call(t, h, http.MethodGet, "/x")

	if calls.Load() != 2 || len(c.Keys()) != 0 {
		t.Errorf("handler ran %d times, cache holds %v; want 2 and nothing", calls.Load(), c.Keys())
	}
}

func TestResponseCacheOffWithoutACache(t *testing.T) {
	var calls atomic.Int32
	for _, rc := range []*handler.ResponseCache{nil, handler.NewResponseCache(nil, time.Minute)} {
		h := rc.Wrap(counting(&calls, http.StatusOK))
		if rec := call(t, h, http.MethodGet, "/x"); rec.Header().Get("X-Cache") != "" {
			t.Errorf("X-Cache = %q, want none", rec.Header().Get("X-Cache"))
		}
	}
}

// Identical requests that miss at the same time must share one computation, or a busy route
// would be recomputed by every waiting client the moment its entry expires.
func TestResponseCacheSharesTheWorkOfSimultaneousMisses(t *testing.T) {
	var calls atomic.Int32
	c := &mock.Cache{}
	release := make(chan struct{})
	slow := func(w http.ResponseWriter, r *http.Request) {
		<-release
		counting(&calls, http.StatusOK)(w, r)
	}
	h := handler.NewResponseCache(c, time.Minute).Wrap(slow)

	const clients = 20
	var wg sync.WaitGroup
	for range clients {
		wg.Go(func() { call(t, h, http.MethodGet, "/x") })
	}
	for c.GetCalls() != clients { // wait until every client has asked the cache and missed
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if calls.Load() > clients/2 {
		t.Errorf("handler ran %d times for %d simultaneous clients, want them to share it", calls.Load(), clients)
	}
}
