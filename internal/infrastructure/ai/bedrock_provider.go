package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// BedrockProvider implements AIProvider using the Amazon Bedrock Converse API
// with automatic model failover within Bedrock.
type BedrockProvider struct {
	client           *bedrockruntime.Client
	modelID          string
	fastModel        string   // Cheap/fast model for simple queries (ModelHint="fast")
	fallbackModels   []string
	maxTokens        int
	temperature      float64
	topP             float64
	guardrailID      string
	guardrailVersion string
	logger           *zap.Logger
	tracer           trace.Tracer
}

// BedrockConfig holds Bedrock-specific configuration.
type BedrockConfig struct {
	AWSConfig        aws.Config
	ModelID          string
	FastModel        string   // Model for simple/fast queries (defaults to Haiku)
	FallbackModels   []string // Tried in order if primary model fails
	MaxTokens        int
	Temperature      float64
	TopP             float64
	GuardrailID      string
	GuardrailVersion string
}

// NewBedrockProvider creates a new Bedrock provider with model failover.
func NewBedrockProvider(cfg *BedrockConfig, logger *zap.Logger) *BedrockProvider {
	client := bedrockruntime.NewFromConfig(cfg.AWSConfig)
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	fallbacks := cfg.FallbackModels
	if len(fallbacks) == 0 {
		// Default failover chain: Claude Sonnet → Haiku (cheap+fast) → Llama (no Anthropic dependency)
		fallbacks = []string{
			"anthropic.claude-3-5-haiku-20241022",
			"meta.llama3-1-70b-instruct-v1:0",
		}
	}
	fastModel := cfg.FastModel
	if fastModel == "" {
		fastModel = "anthropic.claude-3-5-haiku-20241022"
	}
	return &BedrockProvider{
		client:           client,
		modelID:          cfg.ModelID,
		fastModel:        fastModel,
		fallbackModels:   fallbacks,
		maxTokens:        maxTokens,
		temperature:      cfg.Temperature,
		topP:             cfg.TopP,
		guardrailID:      cfg.GuardrailID,
		guardrailVersion: cfg.GuardrailVersion,
		logger:           logger,
		tracer:           otel.Tracer("bedrock-provider"),
	}
}

func (p *BedrockProvider) Name() string { return "bedrock" }

func (p *BedrockProvider) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := p.client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(p.modelID),
		Messages: []types.Message{{
			Role:    types.ConversationRoleUser,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "ping"}},
		}},
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(1)},
	})
	return err == nil
}

func (p *BedrockProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.ChatCompletionWithTools(ctx, req, nil)
}

func (p *BedrockProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	ctx, span := p.tracer.Start(ctx, "bedrock.converse", trace.WithAttributes(
		attribute.String("model", p.modelID),
		attribute.Int("message_count", len(req.Messages)),
	))
	defer span.End()

	// Pick primary model based on hint
	primaryModel := p.modelID
	if req.ModelHint == "fast" {
		primaryModel = p.fastModel
	}

	// Try primary model, then fallbacks (build new slice to avoid mutating p.fallbackModels)
	models := make([]string, 0, 1+len(p.fallbackModels))
	models = append(models, primaryModel)
	models = append(models, p.fallbackModels...)
	var lastErr error

	for i, modelID := range models {
		resp, err := p.converseWithModel(ctx, modelID, req, tools)
		if err == nil {
			if i > 0 {
				p.logger.Info("bedrock: used fallback model",
					zap.String("primary", p.modelID),
					zap.String("used", modelID),
					zap.Int("attempt", i+1),
				)
			}
			span.SetAttributes(
				attribute.String("model_used", modelID),
				attribute.Int("model_attempt", i+1),
				attribute.Int("tokens", resp.TokensUsed),
			)
			return resp, nil
		}

		lastErr = err

		// Only failover on throttling or server errors, not on bad requests
		if pe, ok := err.(*ProviderError); ok && !pe.Retryable {
			span.RecordError(err)
			return nil, err
		}

		p.logger.Warn("bedrock: model failed, trying next",
			zap.String("model", modelID),
			zap.Error(err),
			zap.Int("remaining", len(models)-i-1),
		)
	}

	span.RecordError(lastErr)
	return nil, lastErr
}

func (p *BedrockProvider) converseWithModel(ctx context.Context, modelID string, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	start := time.Now()

	msgs, system := p.buildMessages(req)
	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(modelID),
		Messages: msgs,
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(int32(p.resolveMaxTokens(req))),
			Temperature: aws.Float32(float32(p.temperature)),
			TopP:        aws.Float32(float32(p.topP)),
		},
	}
	if len(system) > 0 {
		input.System = system
	}
	if tc := p.buildToolConfig(tools); tc != nil {
		input.ToolConfig = tc
	}
	if p.guardrailID != "" {
		input.GuardrailConfig = &types.GuardrailConfiguration{
			GuardrailIdentifier: aws.String(p.guardrailID),
			GuardrailVersion:    aws.String(p.guardrailVersion),
		}
	}

	out, err := p.client.Converse(ctx, input)
	if err != nil {
		return nil, p.mapError(err)
	}

	resp := &ChatResponse{
		Provider: "bedrock",
		Model:    modelID,
		Duration: time.Since(start),
	}

	if out.Usage != nil {
		resp.TokensUsed = int(aws.ToInt32(out.Usage.TotalTokens))
	}
	resp.FinishReason = string(out.StopReason)

	if out.Output != nil {
		if msg, ok := out.Output.(*types.ConverseOutputMemberMessage); ok {
			for _, block := range msg.Value.Content {
				switch b := block.(type) {
				case *types.ContentBlockMemberText:
					resp.Content += b.Value
				case *types.ContentBlockMemberToolUse:
					args := make(map[string]interface{})
					if b.Value.Input != nil {
						raw, _ := b.Value.Input.MarshalSmithyDocument()
						json.Unmarshal(raw, &args)
					}
					resp.ToolCalls = append(resp.ToolCalls, ToolCall{
						ID:        aws.ToString(b.Value.ToolUseId),
						Name:      aws.ToString(b.Value.Name),
						Arguments: args,
					})
				}
			}
		}
	}

	return resp, nil
}

// ChatCompletionStream implements StreamProvider via Bedrock ConverseStream.
// Streaming uses the selected model only — failover mid-stream would corrupt state.
func (p *BedrockProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	defer close(ch)

	modelID := p.modelID
	if req.ModelHint == "fast" {
		modelID = p.fastModel
	}

	msgs, system := p.buildMessages(req)
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(modelID),
		Messages: msgs,
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(int32(p.resolveMaxTokens(req))),
			Temperature: aws.Float32(float32(p.temperature)),
			TopP:        aws.Float32(float32(p.topP)),
		},
	}
	if len(system) > 0 {
		input.System = system
	}
	if tc := p.buildToolConfig(tools); tc != nil {
		input.ToolConfig = tc
	}

	out, err := p.client.ConverseStream(ctx, input)
	if err != nil {
		return p.mapError(err)
	}

	stream := out.GetStream()
	defer stream.Close()

	type pendingTool struct {
		id   string
		name string
		json strings.Builder
	}
	var curTool *pendingTool
	var totalTokens int

	for event := range stream.Events() {
		switch e := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if tu, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				curTool = &pendingTool{
					id:   aws.ToString(tu.Value.ToolUseId),
					name: aws.ToString(tu.Value.Name),
				}
			}
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch d := e.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				select {
				case ch <- StreamChunk{Content: d.Value}:
				case <-ctx.Done():
					return ctx.Err()
				}
			case *types.ContentBlockDeltaMemberToolUse:
				if curTool != nil && d.Value.Input != nil {
					curTool.json.WriteString(*d.Value.Input)
				}
			}
		case *types.ConverseStreamOutputMemberContentBlockStop:
			if curTool != nil {
				args := make(map[string]interface{})
				json.Unmarshal([]byte(curTool.json.String()), &args)
				select {
				case ch <- StreamChunk{ToolCalls: []ToolCall{{ID: curTool.id, Name: curTool.name, Arguments: args}}}:
				case <-ctx.Done():
					return ctx.Err()
				}
				curTool = nil
			}
		case *types.ConverseStreamOutputMemberMetadata:
			if e.Value.Usage != nil {
				totalTokens = int(aws.ToInt32(e.Value.Usage.TotalTokens))
			}
		case *types.ConverseStreamOutputMemberMessageStop:
			select {
			case ch <- StreamChunk{Done: true, TokensUsed: totalTokens}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	if err := stream.Err(); err != nil {
		return p.mapError(err)
	}
	return nil
}

// --- helpers ---

func (p *BedrockProvider) buildMessages(req *ChatRequest) ([]types.Message, []types.SystemContentBlock) {
	var system []types.SystemContentBlock
	if req.SystemPrompt != "" {
		system = append(system, &types.SystemContentBlockMemberText{Value: req.SystemPrompt})
	}

	var msgs []types.Message
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, &types.SystemContentBlockMemberText{Value: m.Content})
			continue
		}

		role := types.ConversationRoleUser
		if m.Role == "assistant" {
			role = types.ConversationRoleAssistant
		}

		var content []types.ContentBlock

		if m.Role == "tool" {
			content = append(content, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: aws.String(m.ToolCallID),
					Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: m.Content}},
				},
			})
			role = types.ConversationRoleUser
		} else if m.Content != "" {
			content = append(content, &types.ContentBlockMemberText{Value: m.Content})
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				content = append(content, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(tc.ID),
						Name:      aws.String(tc.Name),
						Input:     document.NewLazyDocument(json.RawMessage(argsJSON)),
					},
				})
			}
		}

		if len(content) == 0 {
			continue
		}

		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, content...)
		} else {
			msgs = append(msgs, types.Message{Role: role, Content: content})
		}
	}

	return msgs, system
}

func (p *BedrockProvider) buildToolConfig(tools []Tool) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}
	var defs []types.Tool
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		defs = append(defs, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(t.Name),
				Description: aws.String(t.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(json.RawMessage(schema))},
			},
		})
	}
	return &types.ToolConfiguration{Tools: defs}
}

func (p *BedrockProvider) resolveMaxTokens(req *ChatRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return p.maxTokens
}

func (p *BedrockProvider) mapError(err error) error {
	msg := err.Error()
	pe := &ProviderError{Provider: "bedrock", Message: msg}
	switch {
	case strings.Contains(msg, "ThrottlingException"):
		pe.Code = ErrorCodeRateLimit
		pe.Retryable = true
	case strings.Contains(msg, "AccessDeniedException"), strings.Contains(msg, "UnrecognizedClientException"):
		pe.Code = ErrorCodeAuthentication
	case strings.Contains(msg, "ValidationException"):
		pe.Code = ErrorCodeInvalidRequest
	case strings.Contains(msg, "ServiceUnavailableException"), strings.Contains(msg, "InternalServerException"):
		pe.Code = ErrorCodeServerError
		pe.Retryable = true
	default:
		pe.Code = ErrorCodeServerError
		pe.Retryable = true
	}
	return pe
}
