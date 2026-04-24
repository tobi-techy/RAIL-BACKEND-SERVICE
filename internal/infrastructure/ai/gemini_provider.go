package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
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
	mu        sync.RWMutex
	lastError error
	lastCheck time.Time
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(config *ProviderConfig, logger *zap.Logger) *GeminiProvider {
	// Create rate limiter based on RPM config; 0 means no local limit
	var limiter *rate.Limiter
	if config.RateLimitRPM > 0 {
		rps := float64(config.RateLimitRPM) / 60.0
		limiter = rate.NewLimiter(rate.Limit(rps), max(config.RateLimitRPM/10, 1))
	} else {
		limiter = rate.NewLimiter(rate.Inf, 1)
	}

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

// IsAvailable checks if Gemini is available without burning tokens.
// Uses the models list endpoint for a lightweight health check.
func (p *GeminiProvider) IsAvailable(ctx context.Context) bool {
	p.mu.RLock()
	cached := p.lastCheck
	lastErr := p.lastError
	p.mu.RUnlock()

	// Cache availability check for 30 seconds
	if time.Since(cached) < 30*time.Second {
		return lastErr == nil
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s&pageSize=1", p.config.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.mu.Lock()
		p.lastError = err
		p.lastCheck = time.Now()
		p.mu.Unlock()
		return false
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.mu.Lock()
		p.lastError = err
		p.lastCheck = time.Now()
		p.mu.Unlock()
		return false
	}
	defer resp.Body.Close()

	var checkErr error
	if resp.StatusCode != http.StatusOK {
		checkErr = fmt.Errorf("health check failed: HTTP %d", resp.StatusCode)
	}

	p.mu.Lock()
	p.lastError = checkErr
	p.lastCheck = time.Now()
	p.mu.Unlock()
	return checkErr == nil
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
	var systemParts []string

	// Gemini uses "user" and "model" roles (not "assistant")
	for _, msg := range req.Messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			// Gemini doesn't have system role, collect for prepending
			systemParts = append(systemParts, msg.Content)
			continue
		}
		if role == "tool" {
			// Gemini doesn't have tool role; send as user with prefix
			role = "user"
		}

		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]string{
				{"text": msg.Content},
			},
		})
	}

	// Prepend system prompt + any system messages to first user message
	prefix := req.SystemPrompt
	for _, sp := range systemParts {
		if prefix != "" {
			prefix += "\n\n"
		}
		prefix += sp
	}
	if prefix != "" && len(contents) > 0 {
		if firstMsg, ok := contents[0]["parts"].([]map[string]string); ok && len(firstMsg) > 0 {
			firstMsg[0]["text"] = prefix + "\n\n" + firstMsg[0]["text"]
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

	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	} else if p.config.Temperature > 0 {
		genConfig["temperature"] = p.config.Temperature
	}

	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	} else if p.config.TopP > 0 {
		genConfig["topP"] = p.config.TopP
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
			name, _ := funcCall["name"].(string)
			if name == "" {
				continue
			}
			toolCall := ToolCall{
				ID:   fmt.Sprintf("call_%d", len(chatResp.ToolCalls)),
				Name: name,
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
		provErr.Retryable = false
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
