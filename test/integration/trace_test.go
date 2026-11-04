//go:build integration

package integration

import (
	"strings"
	"testing"

	"oracle/internal/quote"
	"oracle/pkg/db"
	"oracle/test/fixture"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func attr(span sdktrace.ReadOnlySpan, key string) string {
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			return a.Value.String()
		}
	}
	return ""
}

func TestQueriesBecomeSpansUnderTheRequestSpan(t *testing.T) {
	t.Parallel()
	conn, _ := newDatabase(t)
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	if err := db.Trace(conn, tp); err != nil {
		t.Fatal(err)
	}
	ctx, parent := tp.Tracer("test").Start(t.Context(), "request")

	bars := fixture.Trend("WIN3", 5, fixture.Today(), nil)
	if err := conn.WithContext(ctx).Create(&bars).Error; err != nil { // create
		t.Fatal(err)
	}
	var found []quote.Quote
	if err := conn.WithContext(ctx).Where("ticker = ?", "WIN3").Find(&found).Error; err != nil { // query
		t.Fatal(err)
	}
	var last string
	if err := conn.WithContext(ctx).Raw("SELECT MAX(ticker) FROM quotes").Row().Scan(&last); err != nil { // row
		t.Fatal(err)
	}
	if err := conn.WithContext(ctx).Exec("DELETE FROM quotes WHERE ticker = ?", "WIN3").Error; err != nil { // raw
		t.Fatal(err)
	}
	if err := conn.WithContext(ctx).Where("ticker = ?", "NONE").First(&quote.Quote{}).Error; err == nil { // not found
		t.Fatal("expected no rows")
	}
	parent.End()

	var queries []sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() != "request" {
			queries = append(queries, s)
		}
	}
	if len(queries) < 5 {
		t.Fatalf("got %d query spans, want one per statement (5)", len(queries))
	}
	var sawInsert, sawSelectWithPlaceholder bool
	for _, s := range queries {
		if s.Parent().SpanID() != parent.SpanContext().SpanID() || s.SpanKind() != trace.SpanKindClient {
			t.Errorf("span %q: parent %v kind %v, want a client child of the request", s.Name(), s.Parent().SpanID(), s.SpanKind())
		}
		stmt := attr(s, "db.statement")
		if attr(s, "db.system") != "postgresql" || stmt == "" {
			t.Errorf("span %q lacks db.system or db.statement", s.Name())
		}
		if strings.Contains(stmt, "WIN3") || strings.Contains(stmt, "NONE") {
			t.Errorf("a value leaked into the statement: %s", stmt)
		}
		if s.Status().Code == codes.Error {
			t.Errorf("span %q is an error, but a missing row is not one: %s", s.Name(), s.Status().Description)
		}
		sawInsert = sawInsert || strings.HasPrefix(stmt, "INSERT INTO")
		sawSelectWithPlaceholder = sawSelectWithPlaceholder || (strings.HasPrefix(stmt, "SELECT") && strings.Contains(stmt, "$1"))
	}
	if !sawInsert || !sawSelectWithPlaceholder {
		t.Error("expected an INSERT span and a SELECT span with its placeholder")
	}
}

func TestFailedQueriesAreErrorSpans(t *testing.T) {
	t.Parallel()
	conn, _ := newDatabase(t)
	recorder := tracetest.NewSpanRecorder()
	if err := db.Trace(conn, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))); err != nil {
		t.Fatal(err)
	}

	if err := conn.WithContext(t.Context()).Exec("SELECT * FROM table_that_does_not_exist").Error; err == nil {
		t.Fatal("expected an error")
	}

	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Status().Code != codes.Error || !strings.Contains(spans[0].Status().Description, "table_that_does_not_exist") {
		t.Errorf("spans = %v, want one error span carrying the database error", spans)
	}
}
