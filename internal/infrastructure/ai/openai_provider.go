package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	openAIAPIURL = "https://api.openai.com/v1/chat/completions"
)

// OpenAIProvider implements AIProvider for OpenAI's API
type OpenAIProvider struct {
	config     *ProviderConfig
	client     *http.Client
	logger     *zap.Logger
	tracer     trace.Tracer
	limiter    *rate.Limiter
	lastError  error
	lastCheck  time.Time
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config *ProviderConfig, logger *zap.Logger) *OpenAIProvider {
	// Create rate limiter based on RPM config
	rps := float64(config.RateLimitRPM) / 60.0 // Convert requests per minute to per second
	limiter := rate.NewLimiter(rate.Limit(rps), 1) // Burst of 1

	return &OpenAIProvider{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		logger:  logger,
		tracer:  otel.Tracer("openai-provider"),
		limiter: limiter,
	}
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// IsAvailable checks if OpenAI is available
func (p *OpenAIProvider) IsAvailable(ctx context.Context) bool {
	// Cache availability check for 1 minute
	if time.Since(p.lastCheck) < time.Minute && p.lastError == nil {
		return true
	}

	// Simple health check - just verify we can make a minimal request
	req := &ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "test"},
		},
		MaxTokens: 5,
	}

	_, err := p.ChatCompletion(ctx, req)
	p.lastError = err
	p.lastCheck = time.Now()

	return err == nil
}

// ChatCompletion performs a standard chat completion
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.ChatCompletionWithTools(ctx, req, nil)
}

// ChatCompletionWithTools performs chat completion with optional tool calling
func (p *OpenAIProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	startTime := time.Now()
	ctx, span := p.tracer.Start(ctx, "openai.chat_completion", trace.WithAttributes(
		attribute.Int("message_count", len(req.Messages)),
		attribute.Int("tool_count", len(tools)),
	))
	defer span.End()

	// Wait for rate limiter
	if err := p.limiter.Wait(ctx); err != nil {
		return nil, &ProviderError{
			Provider:  p.Name(),
			Code:      ErrorCodeRateLimit,
			Message:   "rate limit exceeded",
			Retryable: true,
		}
	}

	// Build OpenAI request
	openAIReq := p.buildOpenAIRequest(req, tools)

	// Marshal request
	reqBody, err := json.Marshal(openAIReq)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", openAIAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	// Execute request
	resp, err := p.client.Do(httpReq)
	if err != nil {
		span.RecordError(err)
		return nil, &ProviderError{
			Provider:  p.Name(),
			Code:      ErrorCodeTimeout,
			Message:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle HTTP errors
	if resp.StatusCode != http.StatusOK {
		return nil, p.handleHTTPError(resp.StatusCode, body)
	}

	// Parse OpenAI response
	var openAIResp openAIResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert to ChatResponse
	chatResp := p.convertResponse(&openAIResp, time.Since(startTime))

	span.SetAttributes(
		attribute.Int("tokens_used", chatResp.TokensUsed),
		attribute.String("finish_reason", chatResp.FinishReason),
	)

	p.logger.Debug("OpenAI completion successful",
		zap.Int("tokens", chatResp.TokensUsed),
		zap.Duration("duration", chatResp.Duration),
		zap.String("model", chatResp.Model),
	)

	return chatResp, nil
}

// buildOpenAIRequest converts our ChatRequest to OpenAI's format
func (p *OpenAIProvider) buildOpenAIRequest(req *ChatRequest, tools []Tool) map[string]interface{} {
	messages := make([]map[string]string, 0, len(req.Messages)+1)

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	// Add conversation messages
	for _, msg := range req.Messages {
		messages = append(messages, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	openAIReq := map[string]interface{}{
		"model":    p.config.Model,
		"messages": messages,
	}

	// Add optional parameters
	if req.MaxTokens > 0 {
		openAIReq["max_tokens"] = req.MaxTokens
	} else if p.config.MaxTokens > 0 {
		openAIReq["max_tokens"] = p.config.MaxTokens
	}

	if req.Temperature > 0 {
		openAIReq["temperature"] = req.Temperature
	} else if p.config.Temperature > 0 {
		openAIReq["temperature"] = p.config.Temperature
	}

	// Add tools if provided
	if len(tools) > 0 {
		openAITools := make([]map[string]interface{}, len(tools))
		for i, tool := range tools {
			openAITools[i] = map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.Parameters,
				},
			}
		}
		openAIReq["tools"] = openAITools
		openAIReq["tool_choice"] = "auto"
	}

	return openAIReq
}

// convertResponse converts OpenAI response to our ChatResponse format
func (p *OpenAIProvider) convertResponse(resp *openAIResponse, duration time.Duration) *ChatResponse {
	if len(resp.Choices) == 0 {
		return &ChatResponse{
			Provider: p.Name(),
			Model:    resp.Model,
			Duration: duration,
		}
	}

	choice := resp.Choices[0]
	chatResp := &ChatResponse{
		Content:      choice.Message.Content,
		TokensUsed:   resp.Usage.TotalTokens,
		Provider:     p.Name(),
		FinishReason: choice.FinishReason,
		Model:        resp.Model,
		Duration:     duration,
	}

	// Parse tool calls if present
	if len(choice.Message.ToolCalls) > 0 {
		chatResp.ToolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
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

// handleHTTPError converts HTTP error responses to ProviderError
func (p *OpenAIProvider) handleHTTPError(statusCode int, body []byte) error {
	var errorResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	_ = json.Unmarshal(body, &errorResp)

	provErr := &ProviderError{
		Provider:  p.Name(),
		Message:   errorResp.Error.Message,
		Retryable: false,
	}

	switch statusCode {
	case http.StatusTooManyRequests:
		provErr.Code = ErrorCodeRateLimit
		provErr.Retryable = true
	case http.StatusUnauthorized, http.StatusForbidden:
		provErr.Code = ErrorCodeAuthentication
	case http.StatusBadRequest:
		provErr.Code = ErrorCodeInvalidRequest
	case http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		provErr.Code = ErrorCodeServerError
		provErr.Retryable = true
	default:
		provErr.Code = ErrorCodeUnavailable
	}

	p.logger.Error("OpenAI API error",
		zap.Int("status_code", statusCode),
		zap.String("error_type", errorResp.Error.Type),
		zap.String("error_message", errorResp.Error.Message),
	)

	return provErr
}

// OpenAI API response structures
type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}
