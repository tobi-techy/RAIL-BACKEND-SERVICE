package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func markerAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String(LangfuseObservationTypeKey, "generation")}
}

func TestLLMAwareSampler_ForcesMarkedSpans(t *testing.T) {
	// Base never samples — proves the marker overrides a dropped decision (the
	// production case: low app sample rate / parent HTTP span sampled out).
	s := llmAwareSampler{base: sdktrace.NeverSample()}

	got := s.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		Attributes:    markerAttrs(),
	})
	if got.Decision != sdktrace.RecordAndSample {
		t.Fatalf("marked span: got decision %v, want RecordAndSample", got.Decision)
	}
}

func TestLLMAwareSampler_DefersWhenUnmarked(t *testing.T) {
	if got := (llmAwareSampler{base: sdktrace.NeverSample()}).ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
	}); got.Decision != sdktrace.Drop {
		t.Fatalf("unmarked span with NeverSample base: got %v, want Drop", got.Decision)
	}

	if got := (llmAwareSampler{base: sdktrace.AlwaysSample()}).ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
	}); got.Decision != sdktrace.RecordAndSample {
		t.Fatalf("unmarked span with AlwaysSample base: got %v, want RecordAndSample", got.Decision)
	}
}

// recordingProcessor captures spans the filter decides to forward.
type recordingProcessor struct {
	ended []sdktrace.ReadOnlySpan
}

func (r *recordingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (r *recordingProcessor) OnEnd(s sdktrace.ReadOnlySpan)                   { r.ended = append(r.ended, s) }
func (r *recordingProcessor) Shutdown(context.Context) error                  { return nil }
func (r *recordingProcessor) ForceFlush(context.Context) error                { return nil }

func TestLangfuseFilterProcessor_ForwardsOnlyMarkedMiriamSpans(t *testing.T) {
	rec := &recordingProcessor{}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(&langfuseFilterProcessor{inner: rec}),
	)
	ctx := context.Background()

	// Forwarded: miriam-scope span carrying the marker.
	_, s1 := tp.Tracer("miriam.llm").Start(ctx, "llm.generation", trace.WithAttributes(markerAttrs()...))
	s1.End()

	// Dropped: miriam-scope span without the marker (e.g. miriam.Evaluate).
	_, s2 := tp.Tracer("miriam").Start(ctx, "miriam.Evaluate")
	s2.End()

	// Dropped: marked span from a non-miriam tracer (fast-path scope filter).
	_, s3 := tp.Tracer("db").Start(ctx, "query", trace.WithAttributes(markerAttrs()...))
	s3.End()

	if len(rec.ended) != 1 {
		t.Fatalf("forwarded %d spans, want 1", len(rec.ended))
	}
	if name := rec.ended[0].Name(); name != "llm.generation" {
		t.Fatalf("forwarded span %q, want llm.generation", name)
	}
}
