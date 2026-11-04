package handler

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"oracle/internal/cache"
	"oracle/internal/telemetry"

	"golang.org/x/sync/singleflight"
)

const (
	// maxCached is the biggest body worth keeping: the cache is for answers, not for dumps.
	maxCached = 4 << 20
	// warnEvery limits the log when the cache is down, which would otherwise print once per request.
	warnEvery = 30 * time.Second
)

// ResponseCache answers repeated GETs from a cache for a short time. Only 200 answers are kept,
// under the path and the query, so an error is never replayed. A cache that fails (Redis down,
// slow) is skipped: the route is served as if there were none.
type ResponseCache struct {
	cache    cache.Cache
	ttl      time.Duration
	inflight singleflight.Group // identical misses at the same time share one computation
	lastWarn atomic.Int64       // unix nano of the last warning
}

// NewResponseCache keeps answers for ttl. A nil cache turns it off.
func NewResponseCache(c cache.Cache, ttl time.Duration) *ResponseCache {
	return &ResponseCache{cache: c, ttl: ttl}
}

// Wrap returns next with the cache in front. The X-Cache header tells HIT from MISS.
func (rc *ResponseCache) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if rc == nil || rc.cache == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next(w, r)
			return
		}
		key := r.URL.Path + "?" + r.URL.Query().Encode() // Encode sorts the parameters: ?a=1&b=2 and ?b=2&a=1 are one key

		stored, hit, err := rc.cache.Get(r.Context(), key)
		route := r.Pattern // the route the mux matched, not the URL: few distinct values
		if err != nil {
			rc.warn("read", err)
			telemetry.CacheResult(route, telemetry.CacheError)
		}
		if hit {
			if res, ok := decode(stored); ok {
				telemetry.CacheResult(route, telemetry.CacheHit)
				res.write(w, "HIT")
				return
			}
		}
		if err == nil {
			telemetry.CacheResult(route, telemetry.CacheMiss)
		}

		// The work outlives this request: if the client leaves, the others waiting for the same
		// answer, and the cache, still get it.
		ctx := context.WithoutCancel(r.Context())
		shared, _, _ := rc.inflight.Do(key, func() (any, error) { return rc.compute(ctx, key, next, r.WithContext(ctx)), nil })
		res, ok := shared.(response)
		if !ok {
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		res.write(w, "MISS")
	}
}

// compute runs the handler, and keeps the answer when it is worth keeping.
func (rc *ResponseCache) compute(ctx context.Context, key string, next http.HandlerFunc, r *http.Request) response {
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	next(rec, r)
	res := response{status: rec.status, contentType: rec.header.Get("Content-Type"), body: rec.body.Bytes()}
	if res.status == http.StatusOK && len(res.body) <= maxCached {
		if err := rc.cache.Set(ctx, key, res.encode(), rc.ttl); err != nil {
			rc.warn("write", err)
		}
	}
	return res
}

func (rc *ResponseCache) warn(op string, err error) {
	now := time.Now().UnixNano()
	last := rc.lastWarn.Load()
	if now-last > int64(warnEvery) && rc.lastWarn.CompareAndSwap(last, now) {
		log.Printf("cache %s failed, serving without it: %v", op, err)
	}
}

// response is an answer as the cache keeps it: the content type and the body of a 200.
type response struct {
	status      int
	contentType string
	body        []byte
}

// encode is "<content type>\n<body>". A content type never holds a newline.
func (r response) encode() []byte {
	out := make([]byte, 0, len(r.contentType)+1+len(r.body))
	out = append(out, r.contentType...)
	out = append(out, '\n')
	return append(out, r.body...)
}

func decode(stored []byte) (response, bool) {
	contentType, body, found := bytes.Cut(stored, []byte{'\n'})
	if !found {
		return response{}, false
	}
	return response{status: http.StatusOK, contentType: string(contentType), body: body}, true
}

func (r response) write(w http.ResponseWriter, state string) {
	if r.contentType != "" {
		w.Header().Set("Content-Type", r.contentType)
	}
	w.Header().Set("X-Cache", state)
	w.WriteHeader(r.status)
	_, _ = w.Write(r.body)
}

// recorder keeps what a handler wrote, to hand it to everyone waiting and to the cache.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }
