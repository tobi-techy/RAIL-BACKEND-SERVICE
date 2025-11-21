package tracing

import (
	"context"
	"database/sql"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	dbTracerName = "database"
)

// DBSpanConfig holds configuration for database span creation
type DBSpanConfig struct {
	Operation string // SELECT, INSERT, UPDATE, DELETE
	Table     string
	Query     string
	IncludeQuery bool // Whether to include full query in span (may contain sensitive data)
}

// StartDBSpan creates a new span for database operations
func StartDBSpan(ctx context.Context, cfg DBSpanConfig) (context.Context, trace.Span) {
	tracer := otel.Tracer(dbTracerName)

	spanName := cfg.Operation
	if cfg.Table != "" {
		spanName = cfg.Operation + " " + cfg.Table
	}

	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", cfg.Operation),
	}

	if cfg.Table != "" {
		attrs = append(attrs, attribute.String("db.sql.table", cfg.Table))
	}

	if cfg.IncludeQuery && cfg.Query != "" {
		attrs = append(attrs, attribute.String("db.statement", cfg.Query))
	}

	ctx, span := tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return ctx, span
}

// EndDBSpan ends a database span with appropriate status
func EndDBSpan(span trace.Span, err error, rowsAffected int64) {
	if err != nil {
		span.RecordError(err)
		if err == sql.ErrNoRows {
			span.SetStatus(codes.Ok, "no rows found")
		} else {
			span.SetStatus(codes.Error, err.Error())
		}
	} else {
		span.SetStatus(codes.Ok, "")
	}

	if rowsAffected >= 0 {
		span.SetAttributes(attribute.Int64("db.rows_affected", rowsAffected))
	}

	span.End()
}

// TraceQuery wraps a database query with tracing
func TraceQuery(ctx context.Context, cfg DBSpanConfig, fn func(context.Context) error) error {
	ctx, span := StartDBSpan(ctx, cfg)
	defer span.End()

	start := time.Now()
	err := fn(ctx)
	duration := time.Since(start)

	span.SetAttributes(attribute.Float64("db.query.duration_ms", float64(duration.Milliseconds())))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return err
}

// TraceQueryWithResult wraps a database query with tracing and returns rows affected
func TraceQueryWithResult(ctx context.Context, cfg DBSpanConfig, fn func(context.Context) (sql.Result, error)) (sql.Result, error) {
	ctx, span := StartDBSpan(ctx, cfg)
	defer func() {
		span.End()
	}()

	start := time.Now()
	result, err := fn(ctx)
	duration := time.Since(start)

	span.SetAttributes(attribute.Float64("db.query.duration_ms", float64(duration.Milliseconds())))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return result, err
	}

	if result != nil {
		if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected >= 0 {
			span.SetAttributes(attribute.Int64("db.rows_affected", rowsAffected))
		}
	}

	span.SetStatus(codes.Ok, "")
	return result, nil
}
