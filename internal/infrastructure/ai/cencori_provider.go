package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cencori "github.com/cencori/cencori-go"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	cencoriBaseURL = "https://cencori.com"
	cencoriName    = "cencori"
)

// CencoriConfig holds configuration for the Cencori AI gateway provider.
type CencoriConfig struct {
	APIKey           string
	ModelSmart       string // High-reasoning model (e.g. "gpt-4o", "claude-sonnet-4-20250514")
	ModelFast        string // Fast/cheap model (e.g. "gpt-4o-mini", "gemini-2.0-flash")
	MaxTokens        int
	MaxContextTokens int
	Temperature      float64
	TopP             float64
	Timeout          time.Duration
	RateLimitRPM     int
	BaseURL          string // Override base URL (default: https://cencori.com)
	TraceID          string // Custom trace ID for observability
}

// CencoriProvider implements AIProvider using the official Cencori Go SDK.
//
// Cencori is an AI gateway that provides:
//   - Multi-provider routing (OpenAI, Anthropic, Google, xAI, Mistral, DeepSeek, Meta)
//   - Automatic failover at the gateway level (no SDK flags needed)
//   - PII detection (input + output scanning)
//   - Prompt injection / jailbreak protection
//   - End-user billing (Stripe Connect, wallets)
//   - Circuit breaking for unhealthy providers
//
// The gateway handles retry, failover, and provider selection internally.
// This provider uses the official Go SDK for native Cencori endpoint access.
type CencoriProvider struct {
	client      *cencori.Client
	config      *CencoriConfig
	logger      *zap.Logger
	traceHeader string
	// costGuard, when non-nil, refuses requests that would push a user over
	// their daily/monthly ceiling and records the post-call cost. Set by DI.
	costGuard *Guard
}

// NewCencoriProvider creates a new Cencori AI gateway provider using the official SDK.
func NewCencoriProvider(config *CencoriConfig, logger *zap.Logger) *CencoriProvider {
	if config == nil {
		config = &CencoriConfig{
			ModelSmart: "gpt-4o",
			ModelFast:  "gpt-4o-mini",
			MaxTokens:  4096,
			Timeout:    60 * time.Second,
		}
	}

	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = cencoriBaseURL
	}

	client, err := cencori.NewClient(
		cencori.WithAPIKey(config.APIKey),
		cencori.WithBaseURL(baseURL),
		cencori.WithTimeout(config.Timeout),
	)
	if err != nil {
		logger.Error("Failed to create Cencori client", zap.Error(err))
		// Return a provider that will fail on use
		return &CencoriProvider{
			config: config,
			logger: logger,
		}
	}

	return &CencoriProvider{
		client:      client,
		config:      config,
		logger:      logger,
		traceHeader: config.TraceID,
	}
}

// SetCostGuard attaches a per-user cost ceiling guard. Pass nil to disable.
// Safe to call multiple times (last writer wins).
func (p *CencoriProvider) SetCostGuard(g *Guard) {
	p.costGuard = g
}

// chatGuardAndRecord runs the pre-call ceiling check and the post-call cost
// recording. For streaming responses, the cost is recorded on stream
// completion using the response's total tokens. For non-streaming, on the
// returned response.
func (p *CencoriProvider) checkGuard(ctx context.Context, userID string) error {
	if p.costGuard == nil || !p.costGuard.IsEnabled() || userID == "" {
		return nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		// Unknown user id format — let the call proceed. The billing layer
		// will tag this as anonymous and we don't gate anonymous traffic.
		return nil
	}
	if err := p.costGuard.Allow(ctx, uid); err != nil {
		p.logger.Info("cencori: refusing call — user over cost ceiling",
			zap.String("user_id", userID),
			zap.Error(err),
		)
		return err
	}
	return nil
}

func (p *CencoriProvider) recordCost(ctx context.Context, userID string, tokens int, model string) {
	if p.costGuard == nil || !p.costGuard.IsEnabled() || userID == "" {
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return
	}
	cost := estimateCostUSD(model, tokens)
	p.costGuard.Record(ctx, uid, cost)
}

// estimateCostUSD mirrors entities.EstimateCost but kept local so the
// infrastructure package doesn't pull in the entity-layer pricing table for
// every call. Pricing tracks the model table in internal/domain/entities.
func estimateCostUSD(model string, tokens int) float64 {
	pricePerToken, ok := cencoriModelPricing[model]
	if !ok {
		pricePerToken = 0.00001 // $10/M fallback
	}
	return pricePerToken * float64(tokens)
}

// cencoriModelPricing tracks per-output-token USD cost for Cencori-routed
// models. Update when provider prices change; the entities layer keeps a
// parallel table for billing reports.
var cencoriModelPricing = map[string]float64{
	"gpt-4o":                    0.00001,    // $10/1M
	"gpt-4o-mini":               0.0000006,  // $0.60/1M
	"claude-haiku-4-5":          0.000001,   // $1/1M
	"claude-sonnet-4-5":         0.000015,   // $15/1M
	"claude-sonnet-4-20250514":  0.000015,   // legacy
	"claude-3-5-haiku-20241022": 0.000001,   // legacy
	"claude-3-haiku-20240307":   0.00000125, // legacy
	"gemini-2.0-flash":          0.0000001,  // effectively free
	"gemini-2.5-flash":          0.00000015,
	"kimi-k2.6":                 0.000002,
	"kimi":                      0.000002,
}

// ChatCompletion performs a standard chat completion via Cencori.
func (p *CencoriProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.ChatCompletionWithTools(ctx, req, nil)
}

// ChatCompletionWithTools performs chat completion with optional tool calling via Cencori.
func (p *CencoriProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	if p.client == nil {
		return nil, &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeAuthentication,
			Message:   "Cencori client not initialized",
			Retryable: false,
		}
	}

	// Per-user cost ceiling enforcement. Refuse the call before we hit the
	// gateway if the user is over budget — this is the single most important
	// safeguard for the $20 Cencori balance.
	if err := p.checkGuard(ctx, req.UserID); err != nil {
		return nil, err
	}

	startTime := time.Now()

	// Build Cencori messages
	messages := p.buildMessages(req)

	// Build Cencori tools
	cencoriTools := p.buildTools(tools)

	// Resolve model based on tier (cost optimization)
	model := p.resolveModel(req)

	// Build params
	params := &cencori.ChatParams{
		Model:    model,
		Messages: messages,
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = &req.MaxTokens
	} else if p.config.MaxTokens > 0 {
		params.MaxTokens = &p.config.MaxTokens
	}

	if req.Temperature != nil {
		params.Temperature = req.Temperature
	} else if p.config.Temperature > 0 {
		params.Temperature = &p.config.Temperature
	}

	if req.TopP != nil {
		params.TopP = req.TopP
	} else if p.config.TopP > 0 {
		params.TopP = &p.config.TopP
	}

	// End-user billing
	if req.UserID != "" {
		params.User = &req.UserID
	}

	// Tool calling
	if len(cencoriTools) > 0 {
		params.Tools = cencoriTools
	}

	// Execute request
	resp, err := p.client.Chat.Create(ctx, params)
	if err != nil {
		return nil, p.handleError(err)
	}

	result := p.convertResponse(resp, time.Since(startTime))

	// Record the post-call cost. We use the model's reported total tokens
	// (prompt + completion) since Cencori's pricing is per-token and we don't
	// have a separate breakdown.
	if req.UserID != "" {
		p.recordCost(ctx, req.UserID, result.TokensUsed, result.Model)
	}

	return result, nil
}

// ChatCompletionStream streams via the Cencori SDK.
func (p *CencoriProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	defer close(ch)

	if p.client == nil {
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeAuthentication,
			Message:   "Cencori client not initialized",
			Retryable: false,
		}
	}

	// Per-user cost ceiling enforcement — same gate as the non-streaming path.
	if err := p.checkGuard(ctx, req.UserID); err != nil {
		return err
	}

	// Build Cencori messages
	messages := p.buildMessages(req)

	// Build Cencori tools
	cencoriTools := p.buildTools(tools)

	// Resolve model based on tier
	model := p.resolveModel(req)

	// Build params
	params := &cencori.ChatParams{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = &req.MaxTokens
	} else if p.config.MaxTokens > 0 {
		params.MaxTokens = &p.config.MaxTokens
	}

	if req.Temperature != nil {
		params.Temperature = req.Temperature
	} else if p.config.Temperature > 0 {
		params.Temperature = &p.config.Temperature
	}

	if req.TopP != nil {
		params.TopP = req.TopP
	} else if p.config.TopP > 0 {
		params.TopP = &p.config.TopP
	}

	// End-user billing
	if req.UserID != "" {
		params.User = &req.UserID
	}

	// Tool calling
	if len(cencoriTools) > 0 {
		params.Tools = cencoriTools
	}

	// Start streaming
	stream, err := p.client.Chat.Stream(ctx, params)
	if err != nil {
		return p.handleError(err)
	}

	// Convert stream chunks to our format. The Cencori SDK does not emit
	// usage on each stream chunk, so we accumulate content length and
	// estimate tokens on completion. This is intentionally conservative —
	// we never want to underestimate user cost.
	var contentLen int
	for chunk := range stream {
		if chunk.Err != nil {
			select {
			case ch <- StreamChunk{Done: true, Content: fmt.Sprintf("Stream error: %v", chunk.Err)}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return p.handleError(chunk.Err)
		}

		sc := StreamChunk{}

		if chunk.Delta != "" {
			sc.Content = chunk.Delta
			contentLen += len(chunk.Delta)
		}

		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].FinishReason != nil {
				sc.Done = true
			}
		}

		select {
		case ch <- sc:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Record cost on stream completion. Include input estimate (system
	// prompt + messages) so we don't bill only for output. The estimate is
	// intentionally rough; the Cencori dashboard is the source of truth.
	if req.UserID != "" {
		inputChars := len(req.SystemPrompt)
		for _, m := range req.Messages {
			inputChars += len(m.Content)
		}
		totalTokens := (inputChars + contentLen) / 4
		if totalTokens < 1 {
			totalTokens = 1
		}
		p.recordCost(ctx, req.UserID, totalTokens, model)
	}

	return nil
}

// Name returns "cencori".
func (p *CencoriProvider) Name() string {
	return cencoriName
}

// IsAvailable checks if the Cencori gateway is reachable.
func (p *CencoriProvider) IsAvailable(ctx context.Context) bool {
	if p.client == nil {
		return false
	}

	// Use the smart model for the connectivity ping; cheaper to also test fast,
	// but a single request proves gateway reachability.
	_, err := p.client.Chat.Create(ctx, &cencori.ChatParams{
		Model: p.resolveModel(&ChatRequest{}),
		Messages: []cencori.Message{
			{Role: "user", Content: "ping"},
		},
		MaxTokens: intPtr(1),
	})
	if err == nil {
		return true
	}

	// A rejected key or an exhausted balance means every completion will fail,
	// so reporting "available" here hides the one fact that explains why Miriam
	// answers with her fallback copy. Everything else (rate limit, upstream
	// provider blip, a filter tripping on the ping payload) is transient and
	// says nothing about reachability.
	switch {
	case errors.Is(err, cencori.ErrInvalidAPIKey):
		p.logger.Error("cencori: availability probe failed — API key rejected")
		return false
	case errors.Is(err, cencori.ErrInsufficientCredits):
		p.logger.Error("cencori: availability probe failed — account is out of credits")
		return false
	}
	return true
}

// resolveModel picks the model ID based on the requested tier.
// "fast" routes to the cheap/fast model; everything else uses the smart model.
func (p *CencoriProvider) resolveModel(req *ChatRequest) string {
	if req.ModelHint == "fast" && p.config.ModelFast != "" {
		return p.config.ModelFast
	}
	if p.config.ModelSmart != "" {
		return p.config.ModelSmart
	}
	// Backwards-compatible fallback if someone constructed the config with only
	// the legacy "Model" field (shouldn't happen with the new DI wiring).
	return "gpt-4o"
}

// buildMessages converts our messages to Cencori format.
func (p *CencoriProvider) buildMessages(req *ChatRequest) []cencori.Message {
	var messages []cencori.Message

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		messages = append(messages, cencori.Message{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	// Add conversation messages
	for _, msg := range req.Messages {
		cencoriMsg := cencori.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Handle tool call ID
		if msg.ToolCallID != "" {
			cencoriMsg.ToolCallID = &msg.ToolCallID
		}

		// Handle tool calls
		if len(msg.ToolCalls) > 0 {
			cencoriMsg.ToolCalls = make([]cencori.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				cencoriMsg.ToolCalls[i] = cencori.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: cencori.ToolCallFunction{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				}
			}
		}

		messages = append(messages, cencoriMsg)
	}

	return messages
}

// buildTools converts our tools to Cencori format.
func (p *CencoriProvider) buildTools(tools []Tool) []cencori.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	cencoriTools := make([]cencori.ToolDefinition, len(tools))
	for i, tool := range tools {
		cencoriTools[i] = cencori.ToolDefinition{
			Type: "function",
			Function: cencori.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	}

	return cencoriTools
}

// convertResponse converts Cencori response to our format.
func (p *CencoriProvider) convertResponse(resp *cencori.ChatResponse, duration time.Duration) *ChatResponse {
	chatResp := &ChatResponse{
		Content:      resp.Content,
		TokensUsed:   resp.Usage.TotalTokens,
		Provider:     cencoriName,
		FinishReason: resp.FinishReason,
		Model:        resp.Model,
		Duration:     duration,
	}

	// Handle top-level tool calls
	if len(resp.ToolCalls) > 0 {
		chatResp.ToolCalls = make([]ToolCall, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				p.logger.Warn("Failed to parse tool call arguments", zap.Error(err))
				args = map[string]interface{}{}
			}
			chatResp.ToolCalls[i] = ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			}
		}
	}

	// Handle choices with tool calls (some models return here)
	if len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 && chatResp.ToolCalls == nil {
		chatResp.ToolCalls = make([]ToolCall, len(resp.Choices[0].Message.ToolCalls))
		for i, tc := range resp.Choices[0].Message.ToolCalls {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				p.logger.Warn("Failed to parse tool call arguments", zap.Error(err))
				args = map[string]interface{}{}
			}
			chatResp.ToolCalls[i] = ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			}
		}
	}

	return chatResp
}

// handleError converts Cencori errors to our ProviderError format.
func (p *CencoriProvider) handleError(err error) error {
	if err == nil {
		return nil
	}

	// Check for typed Cencori errors
	switch {
	case errors.Is(err, cencori.ErrInvalidAPIKey):
		p.logger.Error("Cencori: invalid API key")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeAuthentication,
			Message:   "Invalid Cencori API key",
			Retryable: false,
		}

	case errors.Is(err, cencori.ErrSecurityViolation):
		p.logger.Warn("Cencori: security violation (prompt injection blocked)")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeSecurityViolation,
			Message:   "Prompt injection detected and blocked by Cencori",
			Retryable: false,
		}

	case errors.Is(err, cencori.ErrSafetyViolation):
		p.logger.Warn("Cencori: safety violation")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeSecurityViolation,
			Message:   "Content blocked by Cencori safety filter",
			Retryable: false,
		}

	case errors.Is(err, cencori.ErrRateLimited):
		p.logger.Warn("Cencori: rate limited")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeRateLimit,
			Message:   "Rate limited by Cencori",
			Retryable: true,
		}

	case errors.Is(err, cencori.ErrInsufficientCredits):
		p.logger.Error("Cencori: insufficient credits")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeUnavailable,
			Message:   "Insufficient credits",
			Retryable: false,
		}

	case errors.Is(err, cencori.ErrInvalidModel):
		p.logger.Error("Cencori: invalid model", zap.String("model", p.config.ModelSmart))
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeInvalidRequest,
			Message:   fmt.Sprintf("Invalid model: %s", p.config.ModelSmart),
			Retryable: false,
		}

	case errors.Is(err, cencori.ErrProvider):
		p.logger.Warn("Cencori: provider error (upstream failure)")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeServerError,
			Message:   "Upstream provider error",
			Retryable: true,
		}

	// Cencori gateway internal error (HTTP 500 from the gateway itself,
	// not an upstream provider). The SDK returns this as a generic error
	// with message "cencori: internal_error (status: 500)".
	case strings.Contains(err.Error(), "internal_error") && strings.Contains(err.Error(), "status: 500"):
		p.logger.Warn("Cencori: gateway internal error (500)")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeServerError,
			Message:   "Cencori gateway internal error",
			Retryable: true,
		}

	case errors.Is(err, cencori.ErrContentFiltered):
		p.logger.Warn("Cencori: content filtered")
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeSecurityViolation,
			Message:   "Content filtered by Cencori",
			Retryable: false,
		}

	default:
		// Generic error
		p.logger.Error("Cencori: unknown error", zap.Error(err))
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeUnavailable,
			Message:   err.Error(),
			Retryable: true,
		}
	}
}

// intPtr is a helper to get a pointer to an int.
func intPtr(i int) *int {
	return &i
}
