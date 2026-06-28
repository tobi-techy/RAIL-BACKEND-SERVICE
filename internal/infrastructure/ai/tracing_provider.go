package ai

import (
	"context"

	"github.com/rail-service/rail_service/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
	// The observation marker must be a start-time attribute so llmAwareSampler
	// can force-sample this span before any SetAttributes call runs.
	ctx, span := p.tracer.Start(ctx, "llm.generation",
		trace.WithAttributes(attribute.String(tracing.LangfuseObservationTypeKey, "generation")))
	var attrs []attribute.KeyValue
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
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span
}

func (p *tracingProvider) finish(span trace.Span, resp *ChatResponse, err error) {
	if err != nil {
		// gen_ai.system is set exactly once per span; on error we only know the
		// provider we dispatched to.
		span.SetAttributes(attribute.String("gen_ai.system", p.inner.Name()))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	if resp == nil {
		span.SetAttributes(attribute.String("gen_ai.system", p.inner.Name()))
		return
	}
	// resp.Provider is the provider that actually served the request (accurate
	// after failover); fall back to the dispatched provider name if unset.
	system := resp.Provider
	if system == "" {
		system = p.inner.Name()
	}
	span.SetAttributes(
		attribute.String("gen_ai.system", system),
		attribute.String("gen_ai.response.model", resp.Model),
		attribute.Int("gen_ai.usage.total_tokens", resp.TokensUsed),
		attribute.String("gen_ai.response.finish_reason", resp.FinishReason),
		attribute.Int("gen_ai.response.tool_calls", len(resp.ToolCalls)),
	)
	if p.capture && resp.Content != "" {
		span.SetAttributes(attribute.String("gen_ai.completion", resp.Content))
	}
}

// streamingTracingProvider adds StreamProvider support when the inner provider
// streams.
type streamingTracingProvider struct {
	*tracingProvider
	streamer StreamProvider
}

func (p *streamingTracingProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	ctx, span := p.tracer.Start(ctx, "llm.generation.stream",
		trace.WithAttributes(attribute.String(tracing.LangfuseObservationTypeKey, "generation")))
	defer span.End()
	span.SetAttributes(
		attribute.String("gen_ai.system", p.inner.Name()),
		attribute.Bool("gen_ai.streaming", true),
	)
	if p.capture {
		if msg := lastUserMessage(req.Messages); msg != "" {
			span.SetAttributes(attribute.String("gen_ai.prompt", msg))
		}
	}

	// Proxy the stream so we can read token usage off the final chunk without
	// altering the contract: the inner streamer closes `proxy`; we forward each
	// chunk to the caller's `ch` and close `ch` ourselves when the stream ends.
	proxy := make(chan StreamChunk)
	drained := make(chan struct{})
	var tokens int
	go func() {
		defer close(drained)
		defer close(ch)
		for chunk := range proxy {
			if chunk.TokensUsed > tokens {
				tokens = chunk.TokensUsed
			}
			ch <- chunk
		}
	}()

	err := p.streamer.ChatCompletionStream(ctx, req, tools, proxy)
	<-drained

	if tokens > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", tokens))
	}
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
