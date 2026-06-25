package ai

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// observationTypeKey marks a span as a Langfuse LLM observation. Kept in sync
// with pkg/tracing.LangfuseObservationTypeKey (duplicated to avoid importing
// pkg/tracing here).
const observationTypeKey = "langfuse.observation.type"

// NewTracingProvider wraps an AIProvider so every completion emits a GenAI
// "generation" span (model, tokens, latency, provider, tool count) that Langfuse
// ingests. captureContent controls whether prompt/response TEXT is attached —
// keep it false in production (conversation content is financial PII).
//
// It only advertises StreamProvider when the inner provider does, so streaming
// chat keeps working.
func NewTracingProvider(inner AIProvider, captureContent bool) AIProvider {
	base := &tracingProvider{inner: inner, tracer: otel.Tracer("miriam.llm"), capture: captureContent}
	if streamer, ok := inner.(StreamProvider); ok {
		return &streamingTracingProvider{tracingProvider: base, streamer: streamer}
	}
	return base
}

type tracingProvider struct {
	inner   AIProvider
	tracer  trace.Tracer
	capture bool
}

func (p *tracingProvider) Name() string                         { return p.inner.Name() }
func (p *tracingProvider) IsAvailable(ctx context.Context) bool { return p.inner.IsAvailable(ctx) }

func (p *tracingProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	ctx, span := p.startGeneration(ctx, req, 0)
	defer span.End()
	resp, err := p.inner.ChatCompletion(ctx, req)
	p.finish(span, resp, err)
	return resp, err
}

func (p *tracingProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	ctx, span := p.startGeneration(ctx, req, len(tools))
	defer span.End()
	resp, err := p.inner.ChatCompletionWithTools(ctx, req, tools)
	p.finish(span, resp, err)
	return resp, err
}

func (p *tracingProvider) startGeneration(ctx context.Context, req *ChatRequest, toolCount int) (context.Context, trace.Span) {
	ctx, span := p.tracer.Start(ctx, "llm.generation")
	attrs := []attribute.KeyValue{
		attribute.String(observationTypeKey, "generation"),
		attribute.String("gen_ai.system", p.inner.Name()),
	}
	if req.ModelHint != "" {
		attrs = append(attrs, attribute.String("gen_ai.request.model_hint", req.ModelHint))
	}
	if toolCount > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.request.tool_count", toolCount))
	}
	if p.capture {
		if msg := lastUserMessage(req.Messages); msg != "" {
			attrs = append(attrs, attribute.String("gen_ai.prompt", msg))
		}
	}
	span.SetAttributes(attrs...)
	return ctx, span
}

func (p *tracingProvider) finish(span trace.Span, resp *ChatResponse, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	if resp == nil {
		return
	}
	span.SetAttributes(
		attribute.String("gen_ai.response.model", resp.Model),
		attribute.String("gen_ai.system", resp.Provider),
		attribute.Int("gen_ai.usage.total_tokens", resp.TokensUsed),
		attribute.String("gen_ai.response.finish_reason", resp.FinishReason),
		attribute.Int("gen_ai.response.tool_calls", len(resp.ToolCalls)),
	)
	if p.capture && resp.Content != "" {
		span.SetAttributes(attribute.String("gen_ai.completion", resp.Content))
	}
}

// streamingTracingProvider adds StreamProvider support when the inner provider
// streams. The streamed generation span captures latency + provider; token
// usage is captured on the non-streamed tool-round generations.
type streamingTracingProvider struct {
	*tracingProvider
	streamer StreamProvider
}

func (p *streamingTracingProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	ctx, span := p.tracer.Start(ctx, "llm.generation.stream")
	defer span.End()
	span.SetAttributes(
		attribute.String(observationTypeKey, "generation"),
		attribute.String("gen_ai.system", p.inner.Name()),
		attribute.Bool("gen_ai.streaming", true),
	)
	if p.capture {
		if msg := lastUserMessage(req.Messages); msg != "" {
			span.SetAttributes(attribute.String("gen_ai.prompt", msg))
		}
	}
	err := p.streamer.ChatCompletionStream(ctx, req, tools, ch)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
