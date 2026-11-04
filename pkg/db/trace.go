package db

import (
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const spanKey = "oracle:span"

// Trace makes every query a span, a child of the span in the context the query was given, so a
// slow request shows which statement was slow. The statement is recorded with its placeholders,
// never with the values. With no tracer provider configured the spans cost nothing.
//
// It is written here and not taken from a GORM plugin because the plugin drags in the drivers
// of other databases, with their own dependencies to keep patched.
func Trace(conn *gorm.DB, tp trace.TracerProvider) error {
	tracer := tp.Tracer("oracle/db")
	cb := conn.Callback()

	for _, s := range []struct {
		op     string
		before func(string, func(*gorm.DB)) error
		after  func(string, func(*gorm.DB)) error
	}{
		{"query", cb.Query().Before("gorm:query").Register, cb.Query().After("gorm:query").Register},
		{"create", cb.Create().Before("gorm:create").Register, cb.Create().After("gorm:create").Register},
		{"update", cb.Update().Before("gorm:update").Register, cb.Update().After("gorm:update").Register},
		{"delete", cb.Delete().Before("gorm:delete").Register, cb.Delete().After("gorm:delete").Register},
		{"row", cb.Row().Before("gorm:row").Register, cb.Row().After("gorm:row").Register},
		{"raw", cb.Raw().Before("gorm:raw").Register, cb.Raw().After("gorm:raw").Register},
	} {
		if err := s.before("oracle:before_"+s.op, func(tx *gorm.DB) {
			ctx, span := tracer.Start(tx.Statement.Context, "db "+s.op, trace.WithSpanKind(trace.SpanKindClient))
			tx.Statement.Context = ctx
			tx.InstanceSet(spanKey, span)
		}); err != nil {
			return err
		}
		if err := s.after("oracle:after_"+s.op, finish); err != nil {
			return err
		}
	}
	return nil
}

// finish names the span after the table, records the statement and the outcome, and closes it.
func finish(tx *gorm.DB) {
	v, ok := tx.InstanceGet(spanKey)
	if !ok {
		return
	}
	span, ok := v.(trace.Span)
	if !ok {
		return
	}
	defer span.End()
	if table := tx.Statement.Table; table != "" {
		span.SetName("db " + table)
	}
	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", tx.Statement.SQL.String()),
		attribute.Int64("db.rows_affected", tx.RowsAffected),
	)
	if err := tx.Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
