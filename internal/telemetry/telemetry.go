// Package telemetry is how the oracle is watched: Prometheus metrics, structured logs and
// OpenTelemetry traces, all optional and all safe to leave off.
//
// Metrics are always collected and served by MetricsHandler for Prometheus to scrape. Traces and
// logs are sent over OTLP/HTTP only when an endpoint is configured with the standard variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT and
// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT); without them nothing leaves the process.
package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const shutdownTimeout = 5 * time.Second

// Telemetry owns the exporters; Shutdown flushes them.
type Telemetry struct {
	stops []func(context.Context) error
}

// Options say who is reporting.
type Options struct {
	Service     string // service.name, e.g. oracle-api
	Environment string // deployment.environment, e.g. development
	JSONLogs    bool   // JSON lines on stderr instead of text
	LogOutput   io.Writer
}

// Setup installs the logger and, when configured, the exporters. It never fails the program: a
// broken exporter is logged and the program goes on without it.
func Setup(ctx context.Context, opts Options) *Telemetry {
	t := &Telemetry{}
	out := opts.LogOutput
	if out == nil {
		out = os.Stderr
	}
	var stderr slog.Handler = slog.NewTextHandler(out, nil)
	if opts.JSONLogs {
		stderr = slog.NewJSONHandler(out, nil)
	}
	handlers := []slog.Handler{withTrace{stderr}}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", opts.Service),
		attribute.String("deployment.environment.name", opts.Environment),
	))
	if err != nil {
		slog.New(handlers[0]).Warn("telemetry resource", "error", err)
		res = resource.Default()
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	if enabled("TRACES") {
		if exp, err := otlptracehttp.New(ctx); err != nil {
			slog.New(handlers[0]).Warn("traces disabled", "error", err)
		} else {
			tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
			otel.SetTracerProvider(tp)
			t.stops = append(t.stops, tp.Shutdown)
		}
	}
	if enabled("LOGS") {
		if exp, err := otlploghttp.New(ctx); err != nil {
			slog.New(handlers[0]).Warn("log export disabled", "error", err)
		} else {
			lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
			handlers = append(handlers, otelslog.NewHandler(opts.Service, otelslog.WithLoggerProvider(lp)))
			t.stops = append(t.stops, lp.Shutdown)
		}
	}
	// The log package (log.Printf, log.Fatalf) writes through this logger too.
	slog.SetDefault(slog.New(slog.NewMultiHandler(handlers...)))
	return t
}

// enabled reports whether the OTLP endpoint of a signal ("TRACES" or "LOGS") is configured.
func enabled(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}

// Shutdown sends what is still buffered. Call it before the program exits.
func (t *Telemetry) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	errs := make([]error, 0, len(t.stops))
	for _, stop := range t.stops {
		errs = append(errs, stop(ctx))
	}
	if err := errors.Join(errs...); err != nil {
		slog.Warn("telemetry shutdown", "error", err)
	}
}

// JSONLogs reads LOG_FORMAT: "json" for JSON lines, anything else for text.
func JSONLogs() bool { return strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") }

// withTrace adds the trace and span ids of the context to every line, so a line in the terminal
// can be looked up in the traces.
type withTrace struct{ slog.Handler }

func (h withTrace) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h withTrace) WithAttrs(attrs []slog.Attr) slog.Handler {
	return withTrace{h.Handler.WithAttrs(attrs)}
}

func (h withTrace) WithGroup(name string) slog.Handler { return withTrace{h.Handler.WithGroup(name)} }
