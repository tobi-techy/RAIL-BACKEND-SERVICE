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
}

// IsProduction returns true if the environment is production or staging
func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "staging"
}

// InitTracer initializes the OpenTelemetry tracer provider
func InitTracer(ctx context.Context, cfg Config, logger *zap.Logger) (func(context.Context) error, error) {
	if !cfg.Enabled {
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

	// Build gRPC client options based on environment
	grpcOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.CollectorURL),
	}

	// Security: Use TLS by default, only allow insecure in development
	if cfg.IsProduction() {
		// Production/Staging: Always use TLS
		if cfg.Insecure {
			logger.Warn("Insecure gRPC connection requested in production environment - forcing TLS",
				zap.String("environment", cfg.Environment))
		}
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		grpcOpts = append(grpcOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
		logger.Info("OpenTelemetry tracing configured with TLS",
			zap.String("environment", cfg.Environment))
	} else if cfg.Insecure {
		// Development: Allow insecure only if explicitly configured
		grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
		logger.Warn("OpenTelemetry tracing using insecure gRPC connection",
			zap.String("environment", cfg.Environment),
			zap.String("security_note", "This should only be used in development"))
	} else {
		// Development without explicit insecure flag: still use TLS
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		grpcOpts = append(grpcOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
		logger.Info("OpenTelemetry tracing configured with TLS",
			zap.String("environment", cfg.Environment))
	}

	// Create OTLP trace exporter
	traceExporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient(grpcOpts...))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
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

	// Create trace provider with batch span processor
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithSampler(sampler),
	)

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
