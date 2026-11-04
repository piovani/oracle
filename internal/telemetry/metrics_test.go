package telemetry_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oracle/internal/telemetry"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// scrape returns the text of /metrics, as Prometheus would read it.
func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	telemetry.MetricsHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics answered %d", rec.Code)
	}
	return rec.Body.String()
}

// counted is how many times a series with these labels shows in the scrape.
func counted(t *testing.T, series string) float64 {
	t.Helper()
	for line := range strings.SplitSeq(scrape(t), "\n") {
		if strings.HasPrefix(line, series+" ") {
			var v float64
			if _, err := fmt.Sscan(strings.TrimPrefix(line, series+" "), &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	return 0
}

func serveThroughMux(t *testing.T, mux *http.ServeMux, method, target string) {
	t.Helper()
	rec := httptest.NewRecorder()
	telemetry.Instrument(mux).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody))
}

func TestInstrumentLabelsByRoutePatternNotByURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /broken", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	const ok = `oracle_http_requests_total{method="GET",route="GET /things/{id}",status="200"}`
	before := counted(t, ok)

	for _, id := range []string{"1", "2", "3"} { // three URLs, one series
		serveThroughMux(t, mux, http.MethodGet, "/things/"+id)
	}
	serveThroughMux(t, mux, http.MethodGet, "/broken")
	serveThroughMux(t, mux, http.MethodGet, "/nowhere")   // 404 from the mux
	serveThroughMux(t, mux, http.MethodPost, "/things/1") // 405 from the mux

	if got := counted(t, ok) - before; got != 3 {
		t.Errorf("%s grew by %v, want 3", ok, got)
	}
	for _, series := range []string{
		`oracle_http_requests_total{method="GET",route="GET /broken",status="500"}`,
		`oracle_http_requests_total{method="GET",route="unmatched",status="404"}`,
		`oracle_http_requests_total{method="POST",route="unmatched",status="405"}`,
	} {
		if counted(t, series) < 1 {
			t.Errorf("missing %s", series)
		}
	}
	if strings.Contains(scrape(t), `/things/1`) {
		t.Error("a concrete URL became a label value: the cardinality is unbounded")
	}
}

func TestInstrumentRecordsDurationAndReleasesInFlight(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(http.ResponseWriter, *http.Request) {})
	serveThroughMux(t, mux, http.MethodGet, "/slow")

	out := scrape(t)
	if !strings.Contains(out, `oracle_http_request_duration_seconds_count{method="GET",route="GET /slow"} 1`) {
		t.Error("the duration of the request was not observed")
	}
	if counted(t, "oracle_http_requests_in_flight") != 0 {
		t.Error("in-flight requests did not return to zero")
	}
}

func TestInstrumentLogsTheRequestWithItsTraceAndNamesTheSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	var logs bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", LogOutput: &logs, JSONLogs: true})
	t.Cleanup(tel.Shutdown)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Cache", "HIT")
		_, _ = w.Write([]byte("hello"))
	})
	ctx, span := tp.Tracer("test").Start(t.Context(), "outer")
	rec := httptest.NewRecorder()
	telemetry.Instrument(mux).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/things/7", http.NoBody))
	span.End()

	line := logs.String()
	for _, want := range []string{`"msg":"request"`, `"route":"GET /things/{id}"`, `"path":"/things/7"`, `"status":200`, `"cache":"HIT"`, `"bytes":5`, `"trace_id":"` + span.SpanContext().TraceID().String() + `"`} {
		if !strings.Contains(line, want) {
			t.Errorf("log line lacks %s:\n%s", want, line)
		}
	}
	if recorded := recorder.Ended(); len(recorded) != 1 || recorded[0].Name() != "GET /things/{id}" {
		t.Errorf("span name = %v, want the route pattern", names(recorded))
	}
}

func names(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

func TestHealthProbesAreOnlyDebugLogs(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", LogOutput: &logs})
	t.Cleanup(tel.Shutdown)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})

	serveThroughMux(t, mux, http.MethodGet, "/health")

	if logs.Len() != 0 {
		t.Errorf("a health probe was logged at info level: %s", logs.String())
	}
}

func TestOperationTimesAndTraces(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	_, done := telemetry.Operation(context.Background(), "unit_test_op")
	done(nil)
	_, failed := telemetry.Operation(context.Background(), "unit_test_op")
	failed(errors.New("boom"))

	out := scrape(t)
	for _, want := range []string{
		`oracle_operation_duration_seconds_count{operation="unit_test_op",result="ok"} 1`,
		`oracle_operation_duration_seconds_count{operation="unit_test_op",result="error"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	spans := recorder.Ended()
	if len(spans) != 2 || spans[1].Status().Description != "boom" {
		t.Errorf("spans = %v, want two, the second with the error", names(spans))
	}
}

func TestCacheResultCounts(t *testing.T) {
	const hit = `oracle_cache_requests_total{result="hit",route="GET /unit"}`
	before := counted(t, hit)
	telemetry.CacheResult("GET /unit", telemetry.CacheHit)
	telemetry.CacheResult("GET /unit", telemetry.CacheHit)
	telemetry.CacheResult("GET /unit", telemetry.CacheMiss)
	telemetry.CacheResult("GET /unit", telemetry.CacheError)

	if got := counted(t, hit) - before; got != 2 {
		t.Errorf("hits grew by %v, want 2", got)
	}
	for _, r := range []string{"miss", "error"} {
		if counted(t, `oracle_cache_requests_total{result="`+r+`",route="GET /unit"}`) < 1 {
			t.Errorf("no %s counted", r)
		}
	}
}

func TestRegisterDBExportsThePoolAndToleratesRepeats(t *testing.T) {
	pool, err := sql.Open("pgx", "host=127.0.0.1 port=1 user=x dbname=x") // never connects: only its stats are read
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	telemetry.RegisterDB(pool)
	telemetry.RegisterDB(pool) // the second registration is refused and ignored

	if !strings.Contains(scrape(t), "go_sql_open_connections") {
		t.Error("the pool metrics are not exported")
	}
}
