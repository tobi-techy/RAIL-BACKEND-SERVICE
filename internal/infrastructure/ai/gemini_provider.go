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
	geminiAPIURLTemplate = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s"
)

// GeminiProvider implements AIProvider for Google's Gemini API
type GeminiProvider struct {
	config    *ProviderConfig
	client    *http.Client
	logger    *zap.Logger
	tracer    trace.Tracer
	limiter   *rate.Limiter
	lastError error
	lastCheck time.Time
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(config *ProviderConfig, logger *zap.Logger) *GeminiProvider {
	// Create rate limiter based on RPM config
	rps := float64(config.RateLimitRPM) / 60.0
	limiter := rate.NewLimiter(rate.Limit(rps), 1)

	return &GeminiProvider{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		logger:  logger,
		tracer:  otel.Tracer("gemini-provider"),
		limiter: limiter,
	}
}

// Name returns the provider name
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// IsAvailable checks if Gemini is available
func (p *GeminiProvider) IsAvailable(ctx context.Context) bool {
	// Cache availability check for 1 minute
	if time.Since(p.lastCheck) < time.Minute && p.lastError == nil {
		return true
	}

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
func (p *GeminiProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.ChatCompletionWithTools(ctx, req, nil)
}

// ChatCompletionWithTools performs chat completion with optional tool calling
func (p *GeminiProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	startTime := time.Now()
	ctx, span := p.tracer.Start(ctx, "gemini.chat_completion", trace.WithAttributes(
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

	// Build Gemini request
	geminiReq := p.buildGeminiRequest(req, tools)

	// Marshal request
	reqBody, err := json.Marshal(geminiReq)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build API URL
	apiURL := fmt.Sprintf(geminiAPIURLTemplate, p.config.Model, p.config.APIKey)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

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

	// Parse Gemini response
	var geminiResp geminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert to ChatResponse
	chatResp := p.convertResponse(&geminiResp, time.Since(startTime))

	span.SetAttributes(
		attribute.Int("tokens_used", chatResp.TokensUsed),
		attribute.String("finish_reason", chatResp.FinishReason),
	)

	p.logger.Debug("Gemini completion successful",
		zap.Int("tokens", chatResp.TokensUsed),
		zap.Duration("duration", chatResp.Duration),
		zap.String("model", chatResp.Model),
	)

	return chatResp, nil
}

// buildGeminiRequest converts our ChatRequest to Gemini's format
func (p *GeminiProvider) buildGeminiRequest(req *ChatRequest, tools []Tool) map[string]interface{} {
	contents := make([]map[string]interface{}, 0, len(req.Messages))

	// Gemini uses "user" and "model" roles (not "assistant")
	for _, msg := range req.Messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			// Gemini doesn't have system role, prepend to first user message
			continue
		}

		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]string{
				{"text": msg.Content},
			},
		})
	}

	// Prepend system prompt to first user message if present
	if req.SystemPrompt != "" && len(contents) > 0 {
		if firstMsg, ok := contents[0]["parts"].([]map[string]string); ok && len(firstMsg) > 0 {
			firstMsg[0]["text"] = req.SystemPrompt + "\n\n" + firstMsg[0]["text"]
		}
	}

	geminiReq := map[string]interface{}{
		"contents": contents,
	}

	// Add generation config
	genConfig := make(map[string]interface{})
	
	if req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.MaxTokens
	} else if p.config.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = p.config.MaxTokens
	}

	if req.Temperature > 0 {
		genConfig["temperature"] = req.Temperature
	} else if p.config.Temperature > 0 {
		genConfig["temperature"] = p.config.Temperature
	}

	if len(genConfig) > 0 {
		geminiReq["generationConfig"] = genConfig
	}

	// Add tools if provided (Gemini function calling)
	if len(tools) > 0 {
		geminiTools := make([]map[string]interface{}, 0, len(tools))
		functionDeclarations := make([]map[string]interface{}, 0, len(tools))

		for _, tool := range tools {
			functionDeclarations = append(functionDeclarations, map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			})
		}

		geminiTools = append(geminiTools, map[string]interface{}{
			"functionDeclarations": functionDeclarations,
		})

		geminiReq["tools"] = geminiTools
	}

	return geminiReq
}

// convertResponse converts Gemini response to our ChatResponse format
func (p *GeminiProvider) convertResponse(resp *geminiResponse, duration time.Duration) *ChatResponse {
	if len(resp.Candidates) == 0 {
		return &ChatResponse{
			Provider: p.Name(),
			Model:    p.config.Model,
			Duration: duration,
		}
	}

	candidate := resp.Candidates[0]
	chatResp := &ChatResponse{
		Provider:     p.Name(),
		FinishReason: candidate.FinishReason,
		Model:        p.config.Model,
		Duration:     duration,
	}

	// Extract content from parts
	if len(candidate.Content.Parts) > 0 {
		if text, ok := candidate.Content.Parts[0]["text"].(string); ok {
			chatResp.Content = text
		}
	}

	// Parse tool calls if present (function calls in Gemini)
	for _, part := range candidate.Content.Parts {
		if funcCall, ok := part["functionCall"].(map[string]interface{}); ok {
			toolCall := ToolCall{
				ID:   fmt.Sprintf("call_%d", len(chatResp.ToolCalls)),
				Name: funcCall["name"].(string),
			}

			if args, ok := funcCall["args"].(map[string]interface{}); ok {
				toolCall.Arguments = args
			}

			chatResp.ToolCalls = append(chatResp.ToolCalls, toolCall)
		}
	}

	// Token usage
	if resp.UsageMetadata != nil {
		chatResp.TokensUsed = resp.UsageMetadata.TotalTokenCount
	}

	return chatResp
}

// handleHTTPError converts HTTP error responses to ProviderError
func (p *GeminiProvider) handleHTTPError(statusCode int, body []byte) error {
	var errorResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
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

	p.logger.Error("Gemini API error",
		zap.Int("status_code", statusCode),
		zap.String("error_status", errorResp.Error.Status),
		zap.String("error_message", errorResp.Error.Message),
	)

	return provErr
}

// Gemini API response structures
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []map[string]interface{} `json:"parts"`
			Role  string                   `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
}
