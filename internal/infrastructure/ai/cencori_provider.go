package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	cencori "github.com/cencori/cencori-go"
	"go.uber.org/zap"
)

const (
	cencoriBaseURL = "https://cencori.com"
	cencoriName    = "cencori"
)

// CencoriConfig holds configuration for the Cencori AI gateway provider.
type CencoriConfig struct {
	APIKey           string
	Model            string // Model ID to route through Cencori (e.g. "gpt-4o", "claude-sonnet-4-20250514")
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
}

// NewCencoriProvider creates a new Cencori AI gateway provider using the official SDK.
func NewCencoriProvider(config *CencoriConfig, logger *zap.Logger) *CencoriProvider {
	if config == nil {
		config = &CencoriConfig{
			Model:    "gpt-4o",
			MaxTokens: 4096,
			Timeout:  60 * time.Second,
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

	startTime := time.Now()

	// Build Cencori messages
	messages := p.buildMessages(req)

	// Build Cencori tools
	cencoriTools := p.buildTools(tools)

	// Build params
	params := &cencori.ChatParams{
		Model:    p.config.Model,
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

	return p.convertResponse(resp, time.Since(startTime)), nil
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

	// Build Cencori messages
	messages := p.buildMessages(req)

	// Build Cencori tools
	cencoriTools := p.buildTools(tools)

	// Build params
	params := &cencori.ChatParams{
		Model:    p.config.Model,
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

	// Convert stream chunks to our format
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

	// Try a lightweight request to verify connectivity
	_, err := p.client.Chat.Create(ctx, &cencori.ChatParams{
		Model: p.config.Model,
		Messages: []cencori.Message{
			{Role: "user", Content: "ping"},
		},
		MaxTokens: intPtr(1),
	})
	// We don't care about the response, just that we could connect
	return err == nil || !errors.Is(err, cencori.ErrInvalidAPIKey)
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
		p.logger.Error("Cencori: invalid model", zap.String("model", p.config.Model))
		return &ProviderError{
			Provider:  cencoriName,
			Code:      ErrorCodeInvalidRequest,
			Message:   fmt.Sprintf("Invalid model: %s", p.config.Model),
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
