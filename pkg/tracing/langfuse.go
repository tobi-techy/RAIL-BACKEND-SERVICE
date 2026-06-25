package tracing

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// LangfuseObservationTypeKey is the GenAI/Langfuse attribute that marks a span
// as an LLM observation. We use its presence to decide which spans to forward
// to Langfuse (so DB/HTTP/infra spans stay out of the LLM trace view).
const LangfuseObservationTypeKey = "langfuse.observation.type"

// newLangfuseProcessor builds a span processor that exports only LLM-related
// spans to Langfuse over OTLP/HTTP. host is the Langfuse base URL
// (e.g. https://cloud.langfuse.com or a self-hosted instance).
func newLangfuseProcessor(ctx context.Context, host, publicKey, secretKey string) (sdktrace.SpanProcessor, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(host), "/") + "/api/public/otel/v1/traces"
	auth := base64.StdEncoding.EncodeToString([]byte(publicKey + ":" + secretKey))

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                "Basic " + auth,
			"x-langfuse-ingestion-version": "4",
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("langfuse otlp exporter: %w", err)
	}

	return &langfuseFilterProcessor{inner: sdktrace.NewBatchSpanProcessor(exporter)}, nil
}

// langfuseFilterProcessor forwards a span to the wrapped (Langfuse) processor
// only if it carries the Langfuse observation-type marker. Everything else is
// dropped so Langfuse only ever sees Miriam's chat/voice generations.
type langfuseFilterProcessor struct {
	inner sdktrace.SpanProcessor
}

func (p *langfuseFilterProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (p *langfuseFilterProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == LangfuseObservationTypeKey {
			p.inner.OnEnd(s)
			return
		}
	}
}

func (p *langfuseFilterProcessor) Shutdown(ctx context.Context) error { return p.inner.Shutdown(ctx) }
func (p *langfuseFilterProcessor) ForceFlush(ctx context.Context) error {
	return p.inner.ForceFlush(ctx)
}
