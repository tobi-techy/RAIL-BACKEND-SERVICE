package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap.Logger with additional functionality
type Logger struct {
	*zap.SugaredLogger
}

// New creates a new logger instance
func New(level, environment string) *Logger {
	var config zap.Config

	if environment == "production" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// Set log level
	switch level {
	case "debug":
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		config.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Build logger
	logger, err := config.Build()
	if err != nil {
		// Fallback to a basic logger if configuration fails
		baseLogger := zap.NewNop()
		return &Logger{
			SugaredLogger: baseLogger.Sugar(),
		}
	}

	return &Logger{
		SugaredLogger: logger.Sugar(),
	}
}

// Fatal logs a message and then calls os.Exit(1)
func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Fatalw(msg, keysAndValues...)
	os.Exit(1)
}

// WithFields adds fields to the logger context
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	var args []interface{}
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Logger{
		SugaredLogger: l.SugaredLogger.With(args...),
	}
}

// WithError adds an error field to the logger context
func (l *Logger) WithError(err error) *Logger {
	return &Logger{
		SugaredLogger: l.SugaredLogger.With("error", err),
	}
}

// ForRequest creates a logger with request-specific fields
func (l *Logger) ForRequest(requestID, method, path string) *Logger {
	return l.WithFields(map[string]interface{}{
		"request_id": requestID,
		"method":     method,
		"path":       path,
	})
}

// WithTraceID adds trace ID from context to the logger
func (l *Logger) WithTraceID(ctx context.Context) *Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return l
	}

	return l.WithFields(map[string]interface{}{
		"trace_id": span.SpanContext().TraceID().String(),
		"span_id":  span.SpanContext().SpanID().String(),
	})
}

// WithContext adds trace correlation from context (if present)
func (l *Logger) WithContext(ctx context.Context) *Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return l
	}

	return l.WithFields(map[string]interface{}{
		"trace_id": span.SpanContext().TraceID().String(),
		"span_id":  span.SpanContext().SpanID().String(),
	})
}

// CtxDebug logs a debug message with trace correlation
func (l *Logger) CtxDebug(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.WithContext(ctx).Debugw(msg, keysAndValues...)
}

// CtxInfo logs an info message with trace correlation
func (l *Logger) CtxInfo(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.WithContext(ctx).Infow(msg, keysAndValues...)
}

// CtxWarn logs a warning message with trace correlation
func (l *Logger) CtxWarn(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.WithContext(ctx).Warnw(msg, keysAndValues...)
}

// CtxError logs an error message with trace correlation
func (l *Logger) CtxError(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.WithContext(ctx).Errorw(msg, keysAndValues...)
}

// Zap returns the underlying zap.Logger
func (l *Logger) Zap() *zap.Logger {
	return l.SugaredLogger.Desugar()
}

// NewLogger creates a Logger from a zap.Logger
func NewLogger(zapLog *zap.Logger) *Logger {
	return &Logger{
		SugaredLogger: zapLog.Sugar(),
	}
}
