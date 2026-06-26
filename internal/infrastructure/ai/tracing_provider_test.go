package ai

import (
	"context"
	"testing"

	"github.com/rail-service/rail_service/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withSpanRecorder installs a global tracer provider backed by an in-memory
// recorder for the duration of a test and returns the recorder.
func withSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prev := otel.GetTracerProvider()
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

func findAttr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestNewTracingProvider_StreamDetection(t *testing.T) {
	if _, ok := NewTracingProvider(&mockProvider{name: "x"}, false).(StreamProvider); ok {
		t.Fatal("non-streaming provider must not be advertised as StreamProvider")
	}
	if _, ok := NewTracingProvider(&mockStreamProvider{name: "x"}, false).(StreamProvider); !ok {
		t.Fatal("streaming provider must be advertised as StreamProvider")
	}
}

func TestTracingProvider_ChatCompletion_EmitsGenerationSpan(t *testing.T) {
	sr := withSpanRecorder(t)

	inner := &mockProvider{name: "manager", resp: &ChatResponse{
		Content: "hi", Provider: "openai", Model: "gpt-x", TokensUsed: 17, FinishReason: "stop",
	}}
	p := NewTracingProvider(inner, false)

	if _, err := p.ChatCompletion(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	ended := sr.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(ended))
	}
	attrs := ended[0].Attributes()

	if v, ok := findAttr(attrs, tracing.LangfuseObservationTypeKey); !ok || v.AsString() != "generation" {
		t.Fatalf("marker = %v (present=%v), want generation", v.AsString(), ok)
	}
	// gen_ai.system should reflect the provider that served the request, not the
	// dispatching manager — and be present exactly once.
	if v, ok := findAttr(attrs, "gen_ai.system"); !ok || v.AsString() != "openai" {
		t.Fatalf("gen_ai.system = %v (present=%v), want openai", v.AsString(), ok)
	}
	if v, ok := findAttr(attrs, "gen_ai.usage.total_tokens"); !ok || v.AsInt64() != 17 {
		t.Fatalf("total_tokens = %d (present=%v), want 17", v.AsInt64(), ok)
	}
	// Content capture is off by default — no prompt/completion text.
	if _, ok := findAttr(attrs, "gen_ai.completion"); ok {
		t.Fatal("gen_ai.completion must not be set when captureContent is false")
	}
}

// chunkStreamProvider emits a fixed sequence of chunks, mirroring real
// providers' `defer close(ch)` contract.
type chunkStreamProvider struct {
	name   string
	chunks []StreamChunk
}

func (c *chunkStreamProvider) ChatCompletion(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}
func (c *chunkStreamProvider) ChatCompletionWithTools(context.Context, *ChatRequest, []Tool) (*ChatResponse, error) {
	return nil, nil
}
func (c *chunkStreamProvider) Name() string                     { return c.name }
func (c *chunkStreamProvider) IsAvailable(context.Context) bool { return true }
func (c *chunkStreamProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	defer close(ch)
	for _, chunk := range c.chunks {
		ch <- chunk
	}
	return nil
}

func TestStreamingTracingProvider_ForwardsChunksAndCapturesTokens(t *testing.T) {
	sr := withSpanRecorder(t)

	inner := &chunkStreamProvider{name: "openai", chunks: []StreamChunk{
		{Content: "a"},
		{Content: "b"},
		{Done: true, TokensUsed: 42},
	}}
	streamer, ok := NewTracingProvider(inner, false).(StreamProvider)
	if !ok {
		t.Fatal("expected StreamProvider")
	}

	out := make(chan StreamChunk, 8)
	if err := streamer.ChatCompletionStream(context.Background(), &ChatRequest{}, nil, out); err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	// The proxy must forward every chunk and close the caller's channel.
	var got []StreamChunk
	for c := range out {
		got = append(got, c)
	}
	if len(got) != 3 {
		t.Fatalf("forwarded %d chunks, want 3", len(got))
	}
	if got[0].Content != "a" || got[1].Content != "b" || !got[2].Done {
		t.Fatalf("chunks forwarded out of order/incomplete: %+v", got)
	}

	ended := sr.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(ended))
	}
	if v, ok := findAttr(ended[0].Attributes(), "gen_ai.usage.total_tokens"); !ok || v.AsInt64() != 42 {
		t.Fatalf("streaming total_tokens = %d (present=%v), want 42", v.AsInt64(), ok)
	}
}
