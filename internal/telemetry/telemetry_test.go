package telemetry_test

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"oracle/internal/telemetry"

	"go.opentelemetry.io/otel"
)

// collector is an OTLP/HTTP receiver that remembers which paths were posted to.
type collector struct {
	*httptest.Server
	mu    sync.Mutex
	paths map[string]int
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{paths: map[string]int{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths[r.URL.Path]++
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) received(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[path]
}

// keepLogger restores the global logger and tracer provider the test changes.
func keepGlobals(t *testing.T) {
	t.Helper()
	logger, tp := slog.Default(), otel.GetTracerProvider()
	t.Cleanup(func() {
		slog.SetDefault(logger)
		otel.SetTracerProvider(tp)
		log.SetOutput(os.Stderr)
	})
}

func TestSetupWithoutEndpointsSendsNothing(t *testing.T) {
	keepGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	var logs bytes.Buffer

	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", LogOutput: &logs})
	slog.Info("hello", "n", 1)
	tel.Shutdown() // nothing to flush, and it must not hang

	if !strings.Contains(logs.String(), "hello") {
		t.Errorf("the log line did not reach the output: %q", logs.String())
	}
}

func TestSetupSendsTracesAndLogsToTheConfiguredEndpoints(t *testing.T) {
	keepGlobals(t)
	c := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", c.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", c.URL+"/otlp/v1/logs")
	var logs bytes.Buffer

	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", Environment: "ci", LogOutput: &logs})
	_, done := telemetry.Operation(t.Context(), "exported_op")
	done(nil)
	slog.Info("exported line")
	tel.Shutdown() // flushes the batches

	if c.received("/v1/traces") == 0 {
		t.Error("no trace reached the collector")
	}
	if c.received("/otlp/v1/logs") == 0 {
		t.Error("no log reached the collector")
	}
	if !strings.Contains(logs.String(), "exported line") {
		t.Error("exporting must not take the log away from the terminal")
	}
}

func TestSetupWithAnEndpointThatIsDownDoesNotBreakTheProgram(t *testing.T) {
	keepGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	var logs bytes.Buffer

	start := time.Now()
	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", LogOutput: &logs})
	slog.Info("still logging")
	tel.Shutdown()

	if !strings.Contains(logs.String(), "still logging") {
		t.Error("the log line was lost")
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("setup and shutdown took %s with the collector down", took)
	}
}

func TestLogPackageGoesThroughTheStructuredLogger(t *testing.T) {
	keepGlobals(t)
	var logs bytes.Buffer
	tel := telemetry.Setup(t.Context(), telemetry.Options{Service: "test", LogOutput: &logs, JSONLogs: true})
	defer tel.Shutdown()

	log.Printf("old style %d", 42)

	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("not a JSON line: %q (%v)", logs.String(), err)
	}
	if line["msg"] != "old style 42" || line["level"] != "INFO" {
		t.Errorf("line = %v, want the message of log.Printf at info level", line)
	}
}

func TestJSONLogsReadsLogFormat(t *testing.T) {
	for value, want := range map[string]bool{"json": true, "JSON": true, "text": false, "": false, "xml": false} {
		t.Setenv("LOG_FORMAT", value)
		if got := telemetry.JSONLogs(); got != want {
			t.Errorf("LOG_FORMAT=%q: JSONLogs = %v, want %v", value, got, want)
		}
	}
}

// The dashboard is provisioned from a file nobody compiles: this test is what notices a metric
// that was renamed in the code but not in the panels.
func TestDashboardOnlyUsesMetricsThatExist(t *testing.T) {
	raw, err := os.ReadFile("../../monitoring/grafana/dashboards/oracle-api.json")
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("the dashboard is not valid JSON: %v", err)
	}

	// Touch every series so the scrape lists each metric the code defines.
	telemetry.CacheResult("GET /x", telemetry.CacheHit)
	_, done := telemetry.Operation(t.Context(), "x")
	done(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) {})
	serveThroughMux(t, mux, http.MethodGet, "/x")
	exposed := scrape(t)

	// Metrics of the API are the oracle_* ones; the rest come from exporters and are not ours to define.
	name := regexp.MustCompile(`\boracle_[a-z_]+`)
	used := 0
	for _, p := range dash.Panels {
		for _, target := range p.Targets {
			for _, metric := range name.FindAllString(target.Expr, -1) {
				used++
				base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(metric, "_bucket"), "_count"), "_sum")
				if !strings.Contains(exposed, "\n"+base) && !strings.HasPrefix(exposed, base) {
					t.Errorf("panel %q uses %s, which the API does not export", p.Title, metric)
				}
			}
		}
	}
	if used == 0 {
		t.Fatal("the dashboard uses no oracle_* metric: is the file the right one?")
	}
}
