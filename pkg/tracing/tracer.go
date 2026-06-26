package tracing

import (
	"context"
	"crypto/tls"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
)

const (
	serviceName    = "rail-service"
	serviceVersion = "1.0.0"
)

// Config holds tracing configuration
type Config struct {
	Enabled      bool
	CollectorURL string  // OTLP collector endpoint
	Environment  string  // development, staging, production
	SampleRate   float64 // 0.0 to 1.0
	Insecure     bool    // Allow insecure connection (only for development)

	// Langfuse LLM observability (optional). When public+secret keys are set, a
	// second OTLP/HTTP exporter ships LLM spans to Langfuse alongside the main
	// collector. gRPC is intentionally not used here — Langfuse is HTTP-only.
	LangfusePublicKey string
	LangfuseSecretKey string
	LangfuseHost      string
}

// IsProduction returns true if the environment is production or staging
func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "staging"
}

// InitTracer initializes the OpenTelemetry tracer provider
func InitTracer(ctx context.Context, cfg Config, logger *zap.Logger) (func(context.Context) error, error) {
	langfuseConfigured := cfg.LangfusePublicKey != "" && cfg.LangfuseSecretKey != ""

	if !cfg.Enabled && !langfuseConfigured {
		logger.Info("OpenTelemetry tracing is disabled")
		// Set up no-op tracer
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	// Create resource with service information
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Determine sampler based on sample rate
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// LLM spans must be captured at 100% for observability/cost/eval, so force
	// them through regardless of the (often low) app-trace sample rate. Only
	// applied when Langfuse is configured; everything else keeps the base rate.
	if langfuseConfigured {
		sampler = llmAwareSampler{base: sampler}
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}

	// Main OTLP/gRPC collector (general app traces)
	if cfg.Enabled {
		grpcOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.CollectorURL),
		}
		// Security: Use TLS by default, only allow insecure in development
		if cfg.IsProduction() {
			if cfg.Insecure {
				logger.Warn("Insecure gRPC connection requested in production environment - forcing TLS",
					zap.String("environment", cfg.Environment))
			}
			grpcOpts = append(grpcOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
		} else if cfg.Insecure {
			grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
			logger.Warn("OpenTelemetry tracing using insecure gRPC connection",
				zap.String("environment", cfg.Environment),
				zap.String("security_note", "This should only be used in development"))
		} else {
			grpcOpts = append(grpcOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
		}
		traceExporter, expErr := otlptrace.New(ctx, otlptracegrpc.NewClient(grpcOpts...))
		if expErr != nil {
			return nil, fmt.Errorf("failed to create trace exporter: %w", expErr)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(traceExporter))
		logger.Info("OpenTelemetry collector exporter configured", zap.String("collector_url", cfg.CollectorURL))
	}

	// Langfuse LLM observability (OTLP/HTTP, filtered to LLM spans only)
	if langfuseConfigured {
		lfProc, lfErr := newLangfuseProcessor(ctx, cfg.LangfuseHost, cfg.LangfusePublicKey, cfg.LangfuseSecretKey)
		if lfErr != nil {
			logger.Warn("Langfuse exporter init failed — LLM tracing disabled", zap.Error(lfErr))
		} else {
			tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(lfProc))
			logger.Info("Langfuse LLM tracing enabled", zap.String("host", cfg.LangfuseHost))
		}
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(tpOpts...)

	// Set global trace provider
	otel.SetTracerProvider(tp)

	// Set global propagator to W3C Trace Context and Baggage
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	logger.Info("OpenTelemetry tracing initialized",
		zap.String("collector_url", cfg.CollectorURL),
		zap.Float64("sample_rate", cfg.SampleRate),
		zap.String("environment", cfg.Environment),
		zap.Bool("tls_enabled", !cfg.Insecure || cfg.IsProduction()))

	// Return shutdown function
	return tp.Shutdown, nil
}

// GetTracer returns a tracer for the given name
func GetTracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
