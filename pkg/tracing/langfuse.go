package tracing

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// LangfuseObservationTypeKey is the GenAI/Langfuse attribute that marks a span
// as an LLM observation. We use its presence to decide which spans to forward
// to Langfuse (so DB/HTTP/infra spans stay out of the LLM trace view).
//
// Emitters MUST set this as a start-time attribute (trace.WithAttributes at
// Start) — llmAwareSampler reads it at sampling time, which happens before any
// span.SetAttributes call.
const LangfuseObservationTypeKey = "langfuse.observation.type"

// llmTracerScopePrefix is the instrumentation-scope prefix shared by every
// tracer that emits LLM observations (miriam.chat, miriam.llm). The filter uses
// it as an O(1) fast path; the observation-type marker remains the real gate
// (the "miriam" Evaluate tracer shares the prefix but isn't an LLM span).
const llmTracerScopePrefix = "miriam"

// llmAwareSampler forces sampling for spans carrying the Langfuse observation
// marker, so LLM observability captures 100% of Miriam's generations regardless
// of the lower app-trace sample rate or a parent HTTP span that was sampled out.
// Every other span defers to the wrapped base sampler.
type llmAwareSampler struct {
	base sdktrace.Sampler
}

func (s llmAwareSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	for _, a := range p.Attributes {
		if string(a.Key) == LangfuseObservationTypeKey {
			return sdktrace.SamplingResult{
				Decision:   sdktrace.RecordAndSample,
				Tracestate: trace.SpanContextFromContext(p.ParentContext).TraceState(),
			}
		}
	}
	return s.base.ShouldSample(p)
}

func (s llmAwareSampler) Description() string {
	return "LLMAwareSampler+" + s.base.Description()
}

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
	// Fast path: only spans from Miriam's LLM tracers can be observations, so
	// drop the bulk of app spans (DB/HTTP/infra) without scanning attributes.
	if !strings.HasPrefix(s.InstrumentationScope().Name, llmTracerScopePrefix) {
		return
	}
	// Confirm the marker — a shared-prefix tracer (e.g. miriam.Evaluate) isn't
	// necessarily an LLM observation.
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
