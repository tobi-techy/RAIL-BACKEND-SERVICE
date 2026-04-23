package ai

import (
	"context"
	"time"
)

// AIProvider defines the interface for AI completion providers (OpenAI, Gemini, etc.)
type AIProvider interface {
	// ChatCompletion performs a standard chat completion
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatCompletionWithTools performs chat completion with function/tool calling support
	ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error)

	// Name returns the provider name (e.g., "openai", "gemini")
	Name() string

	// IsAvailable checks if the provider is currently available
	IsAvailable(ctx context.Context) bool
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Messages     []Message `json:"messages"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	MaxTokens    int       `json:"max_tokens,omitempty"`
	Temperature  *float64  `json:"temperature,omitempty"` // nil = use provider default
	TopP         *float64  `json:"top_p,omitempty"`       // nil = use provider default
	UserID       string    `json:"user_id,omitempty"`     // For tracking and rate limiting
}

// Float64 returns a pointer to v. Useful for setting *float64 fields in ChatRequest.
func Float64(v float64) *float64 { return &v }

// Message represents a single message in a conversation
type Message struct {
	Role       string     `json:"role"`    // "user", "assistant", "system", "tool"
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`         // Function name for role="tool" (improves provider compatibility)
	ToolCallID string     `json:"tool_call_id,omitempty"` // Required for role="tool"
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // Required on assistant messages that invoke tools
}

// ChatResponse represents the response from a chat completion
type ChatResponse struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	TokensUsed   int        `json:"tokens_used"`
	Provider     string     `json:"provider"`
	FinishReason string     `json:"finish_reason"` // "stop", "length", "tool_calls", etc.
	Model        string     `json:"model"`
	Duration     time.Duration `json:"duration"`
}

// Tool represents a function/tool that the AI can call
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall represents a tool invocation from the AI
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ProviderConfig holds configuration for AI providers
type ProviderConfig struct {
	APIKey       string
	BaseURL      string // Custom API base URL (for OpenAI-compatible providers like Kimi)
	Model        string
	MaxTokens    int
	Temperature  float64
	TopP         float64
	Timeout      time.Duration
	RateLimitRPM int // Requests per minute
	ProviderName string // Override provider name (e.g. "kimi" instead of "openai")
}

// ProviderError represents an error from an AI provider
type ProviderError struct {
	Provider string
	Code     string
	Message  string
	Retryable bool
}

func (e *ProviderError) Error() string {
	return e.Provider + ": " + e.Message
}

// Common error codes
const (
	ErrorCodeRateLimit       = "rate_limit"
	ErrorCodeInvalidRequest  = "invalid_request"
	ErrorCodeAuthentication  = "authentication"
	ErrorCodeServerError     = "server_error"
	ErrorCodeTimeout         = "timeout"
	ErrorCodeUnavailable     = "unavailable"
)

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Done       bool       `json:"done"`
	TokensUsed int        `json:"tokens_used,omitempty"`
}

// StreamProvider is an optional interface for providers that support streaming.
type StreamProvider interface {
	ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error
}
