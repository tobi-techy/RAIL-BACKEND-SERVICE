package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// OpenAIProvider implements AIProvider for OpenAI's API and OpenAI-compatible providers (Kimi, etc.)
type OpenAIProvider struct {
	config    *ProviderConfig
	client    *http.Client
	logger    *zap.Logger
	tracer    trace.Tracer
	limiter   *rate.Limiter
	mu        sync.RWMutex
	lastError error
	lastCheck time.Time
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config *ProviderConfig, logger *zap.Logger) *OpenAIProvider {
	// Create rate limiter based on RPM config; 0 means no local limit
	var limiter *rate.Limiter
	if config.RateLimitRPM > 0 {
		rps := float64(config.RateLimitRPM) / 60.0
		limiter = rate.NewLimiter(rate.Limit(rps), max(config.RateLimitRPM/10, 1))
	} else {
		limiter = rate.NewLimiter(rate.Inf, 1)
	}

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
	if p.config.ProviderName != "" {
		return p.config.ProviderName
	}
	return "openai"
}

// apiBaseURL returns the normalized base URL for API calls.
func (p *OpenAIProvider) apiBaseURL() string {
	if p.config.BaseURL != "" {
		return strings.TrimSuffix(p.config.BaseURL, "/")
	}
	return "https://api.openai.com/v1"
}

// IsAvailable checks if the provider is available without burning tokens.
// Returns false on authentication errors so operators know the key is bad.
func (p *OpenAIProvider) IsAvailable(ctx context.Context) bool {
	p.mu.RLock()
	cached := p.lastCheck
	lastErr := p.lastError
	p.mu.RUnlock()

	// Cache availability check for 30 seconds to avoid hammering the API
	if time.Since(cached) < 30*time.Second {
		return lastErr == nil
	}

	// Lightweight health check: just verify the API is reachable
	apiURL := p.apiBaseURL() + "/models"

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.mu.Lock()
		p.lastError = err
		p.lastCheck = time.Now()
		p.mu.Unlock()
		return false
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)

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
		if err == context.Canceled || err == context.DeadlineExceeded {
			return nil, err
		}
		return nil, &ProviderError{
			Provider:  p.Name(),
			Code:      ErrorCodeRateLimit,
			Message:   "rate limit exceeded",
			Retryable: true,
		}
	}

	// Truncate messages if provider has a context token limit
	if p.config.MaxContextTokens > 0 {
		req.Messages = truncateMessages(req.Messages, req.SystemPrompt, tools, p.config.MaxContextTokens)
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
	apiURL := p.apiBaseURL() + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
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
		return nil, p.handleHTTPError(resp.StatusCode, body, resp.Header)
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

// ChatCompletionStream streams chat completion chunks via SSE.
//
// NOTE: This implementation does NOT accumulate incremental tool call arguments
// across chunks. Each chunk with tool_calls is emitted as a complete ToolCall.
// This is acceptable for the current orchestrator flow (streaming is only used
// for the final text response, never for tool-calling rounds), but callers
// streaming WITH tools enabled may get incomplete tool call data.
func (p *OpenAIProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	defer close(ch)

	if err := p.limiter.Wait(ctx); err != nil {
		if err == context.Canceled || err == context.DeadlineExceeded {
			return err
		}
		return &ProviderError{
			Provider:  p.Name(),
			Code:      ErrorCodeRateLimit,
			Message:   "rate limit exceeded",
			Retryable: true,
		}
	}

	openAIReq := p.buildOpenAIRequest(req, tools)
	openAIReq["stream"] = true
	// stream_options is an OpenAI-specific extension; omit it for other
	// OpenAI-compatible providers (e.g., Kimi) to avoid strict-rejection bugs.
	if p.config.ProviderName == "" || p.config.ProviderName == "openai" {
		openAIReq["stream_options"] = map[string]interface{}{"include_usage": true}
	}

	reqBody, err := json.Marshal(openAIReq)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	apiURL := p.apiBaseURL() + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return &ProviderError{
			Provider:  p.Name(),
			Code:      ErrorCodeTimeout,
			Message:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return p.handleHTTPError(resp.StatusCode, body, resp.Header)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	var totalTokens int
	var sentDone bool

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			totalTokens = chunk.Usage.TotalTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		sc := StreamChunk{}

		if choice.Delta.Content != "" {
			sc.Content = choice.Delta.Content
		}

		if len(choice.Delta.ToolCalls) > 0 {
			sc.ToolCalls = make([]ToolCall, len(choice.Delta.ToolCalls))
			for i, tc := range choice.Delta.ToolCalls {
				var args map[string]interface{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				if args == nil {
					args = map[string]interface{}{}
				}
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", i)
				}
				sc.ToolCalls[i] = ToolCall{
					ID:        id,
					Name:      tc.Function.Name,
					Arguments: args,
				}
			}
		}

		if choice.FinishReason != "" {
			sc.Done = true
			sc.TokensUsed = totalTokens
			sentDone = true
		}

		select {
		case ch <- sc:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Ensure a final done chunk is sent so consumers know the stream ended.
	// Some providers don't emit finish_reason on the last chunk.
	if !sentDone {
		select {
		case ch <- StreamChunk{Done: true, TokensUsed: totalTokens}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return scanner.Err()
}

// buildOpenAIRequest converts our ChatRequest to OpenAI's format
func (p *OpenAIProvider) buildOpenAIRequest(req *ChatRequest, tools []Tool) map[string]interface{} {
	messages := make([]map[string]interface{}, 0, len(req.Messages)+1)

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	// Add conversation messages.
	//
	// Tool-call IDs must be internally consistent within a single request: every
	// assistant tool_call needs a non-empty id, and every following tool result must
	// carry a matching tool_call_id. Otherwise strict providers (Kimi/Moonshot) reject
	// the request with "tool_call_id is not found". We backfill any missing ids here and
	// hand them to the paired tool messages (which follow in order) as a safety net.
	synthCounter := 0
	var pendingToolCallIDs []string
	for _, msg := range req.Messages {
		content := msg.Content
		if msg.Role == "assistant" && strings.TrimSpace(content) == "" {
			if len(msg.ToolCalls) == 0 {
				p.logger.Warn("skipping empty assistant message in OpenAI request")
				continue
			}
			content = "Calling tools..."
		}
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": content,
		}
		if msg.Role == "tool" {
			tcid := msg.ToolCallID
			if tcid == "" && len(pendingToolCallIDs) > 0 {
				tcid = pendingToolCallIDs[0]
				pendingToolCallIDs = pendingToolCallIDs[1:]
			}
			if tcid != "" {
				m["tool_call_id"] = tcid
				if msg.Name != "" {
					m["name"] = msg.Name
				}
			}
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			if msg.ReasoningContent != "" {
				m["reasoning_content"] = msg.ReasoningContent
			}
			pendingToolCallIDs = pendingToolCallIDs[:0]
			toolCalls := make([]map[string]interface{}, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", synthCounter)
					synthCounter++
				}
				pendingToolCallIDs = append(pendingToolCallIDs, id)
				args, _ := json.Marshal(tc.Arguments)
				toolCalls[i] = map[string]interface{}{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": string(args),
					},
				}
			}
			m["tool_calls"] = toolCalls
		}
		messages = append(messages, m)
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

	// Kimi (Moonshot) API requires temperature=1 for some models (e.g. reasoning models).
	// Attempting any other value results in: "invalid temperature: only 1 is allowed for this model"
	if p.Name() == "kimi" {
		openAIReq["temperature"] = 1.0
	} else {
		if req.Temperature != nil {
			openAIReq["temperature"] = *req.Temperature
		} else if p.config.Temperature > 0 {
			openAIReq["temperature"] = p.config.Temperature
		}
	}

	if req.TopP != nil {
		openAIReq["top_p"] = *req.TopP
	} else if p.config.TopP > 0 {
		openAIReq["top_p"] = p.config.TopP
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
		// tool_choice defaults to "auto" on OpenAI; omitting improves compatibility
		// with some OpenAI-compatible providers that don't accept this parameter.
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
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		TokensUsed:       resp.Usage.TotalTokens,
		Provider:         p.Name(),
		FinishReason:     choice.FinishReason,
		Model:            resp.Model,
		Duration:         duration,
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

			// Some OpenAI-compatible providers (notably Kimi/Moonshot) return tool
			// calls with an empty id. If we echo an empty id back on the follow-up
			// round, the tool result message loses its tool_call_id and the provider
			// rejects the request with "tool_call_id is not found". Synthesize a
			// stable id so the assistant tool_call and its tool result always match.
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", i)
			}
			chatResp.ToolCalls[i] = ToolCall{
				ID:        id,
				Name:      strings.TrimSuffix(tc.Function.Name, "{}"),
				Arguments: args,
			}
		}
	}

	return chatResp
}

// handleHTTPError converts HTTP error responses to ProviderError.
func (p *OpenAIProvider) handleHTTPError(statusCode int, body []byte, headers ...http.Header) error {
	// Try OpenAI-style error first
	var openAIErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	message := ""
	if err := json.Unmarshal(body, &openAIErr); err == nil && openAIErr.Error.Message != "" {
		message = openAIErr.Error.Message
	} else {
		// Fallback: try generic JSON error or raw body
		var genericErr map[string]interface{}
		if err := json.Unmarshal(body, &genericErr); err == nil {
			if msg, ok := genericErr["message"].(string); ok && msg != "" {
				message = msg
			} else if msg, ok := genericErr["error"].(string); ok && msg != "" {
				message = msg
			} else {
				message = string(body)
			}
		} else {
			message = string(body)
		}
	}

	provErr := &ProviderError{
		Provider:  p.Name(),
		Message:   message,
		Retryable: false,
	}

	switch statusCode {
	case http.StatusTooManyRequests:
		provErr.Code = ErrorCodeRateLimit
		provErr.Retryable = true
		provErr.RetryAfter = retryAfterDuration(message, headers...)
	case http.StatusUnauthorized, http.StatusForbidden:
		provErr.Code = ErrorCodeAuthentication
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		provErr.Code = ErrorCodeInvalidRequest
	case http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		provErr.Code = ErrorCodeServerError
		provErr.Retryable = true
	default:
		provErr.Code = ErrorCodeUnavailable
	}

	fields := []zap.Field{
		zap.String("provider", p.Name()),
		zap.Int("status_code", statusCode),
		zap.String("error_message", message),
	}
	if provErr.RetryAfter > 0 {
		fields = append(fields, zap.Duration("retry_after", provErr.RetryAfter))
	}
	if provErr.Retryable {
		p.logger.Warn("AI provider API error", fields...)
	} else {
		p.logger.Error("AI provider API error", fields...)
	}

	return provErr
}

func retryAfterDuration(message string, headers ...http.Header) time.Duration {
	for _, header := range headers {
		if header == nil {
			continue
		}
		value := strings.TrimSpace(header.Get("Retry-After"))
		if value == "" {
			continue
		}
		if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
			return time.Duration(seconds * float64(time.Second))
		}
		if retryAt, err := http.ParseTime(value); err == nil {
			if delay := time.Until(retryAt); delay > 0 {
				return delay
			}
		}
	}

	const marker = "try again after "
	lowerMessage := strings.ToLower(message)
	idx := strings.Index(lowerMessage, marker)
	if idx == -1 {
		return 0
	}
	remainder := message[idx+len(marker):]
	fields := strings.Fields(remainder)
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.Trim(fields[0], ","), 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
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
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
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

type openAIStreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}
