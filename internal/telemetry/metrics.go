package telemetry

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Cache results, the values of the result label of oracle_cache_requests_total.
const (
	CacheHit   = "hit"
	CacheMiss  = "miss"
	CacheError = "error" // Redis failed: the route was served without the cache
)

// labelRoute is the label that carries the route pattern.
const labelRoute = "route"

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "oracle_http_requests_total",
		Help: "HTTP requests handled, by method, route pattern and status code.",
	}, []string{"method", labelRoute, "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "oracle_http_request_duration_seconds",
		Help:    "Time to handle an HTTP request, by method and route pattern.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", labelRoute})

	httpInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "oracle_http_requests_in_flight",
		Help: "HTTP requests being handled right now.",
	})

	cacheRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "oracle_cache_requests_total",
		Help: "Lookups in the response cache, by route pattern and result (hit, miss, error).",
	}, []string{labelRoute, "result"})

	operationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "oracle_operation_duration_seconds",
		Help:    "Time of the heavy operations behind the routes (history_load, plan_build, strategy_scan), by result.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"operation", "result"})
)

// MetricsHandler serves the metrics in the Prometheus text format.
func MetricsHandler() http.Handler { return promhttp.Handler() }

// RegisterDB exports the connection pool of the database (open, in use, idle, wait time).
// Calling it twice for the same pool is harmless.
func RegisterDB(pool *sql.DB) {
	_ = prometheus.Register(collectors.NewDBStatsCollector(pool, "oracle"))
}

// CacheResult counts one lookup in the response cache.
func CacheResult(route, result string) { cacheRequests.WithLabelValues(route, result).Inc() }

// Operation times a heavy operation and traces it. Call the returned function when it ends,
// with its error (or nil): it records the duration and closes the span.
func Operation(ctx context.Context, name string) (context.Context, func(err error)) {
	start := time.Now()
	ctx, span := otel.Tracer("oracle").Start(ctx, name)
	return ctx, func(err error) {
		result := "ok"
		if err != nil {
			result = "error"
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		operationDuration.WithLabelValues(name, result).Observe(time.Since(start).Seconds())
	}
}

// Instrument measures every request that reaches it: count, duration, in-flight, one log line,
// and the name of the trace span. The route is the pattern the mux matched ("GET /tickers/{ticker}"),
// never the URL, so the labels stay few. It must wrap the mux directly: the mux records the
// pattern on the request it was given, and a layer that copies the request would hide it.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		httpInFlight.Inc()
		defer httpInFlight.Dec()

		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched" // 404 and 405 answers of the mux
		}
		elapsed := time.Since(start)
		httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())

		span := trace.SpanFromContext(r.Context())
		span.SetName(route)
		span.SetAttributes(attribute.String("http.route", route))

		level := slog.LevelInfo
		if r.URL.Path == "/health" {
			level = slog.LevelDebug // probes would drown everything else
		}
		slog.Log(r.Context(), level, "request",
			"method", r.Method, "route", route, "path", r.URL.Path, "status", rec.status,
			"duration_ms", float64(elapsed.Microseconds())/1000, "cache", rec.Header().Get("X-Cache"), "bytes", rec.bytes)
	})
}

// statusWriter remembers the status and the size of the answer.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
