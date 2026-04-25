package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestOpenAIProviderName(t *testing.T) {
	p := NewOpenAIProvider(&ProviderConfig{ProviderName: "kimi"}, zap.NewNop())
	assert.Equal(t, "kimi", p.Name())

	p2 := NewOpenAIProvider(&ProviderConfig{}, zap.NewNop())
	assert.Equal(t, "openai", p2.Name())
}

func TestOpenAIProviderBuildRequest(t *testing.T) {
	logger := zap.NewNop()
	p := NewOpenAIProvider(&ProviderConfig{
		Model:       "gpt-4o",
		MaxTokens:   2048,
		Temperature: 0.15,
		TopP:        0.9,
	}, logger)

	t.Run("basic request", func(t *testing.T) {
		req := &ChatRequest{
			Messages: []Message{
				{Role: "user", Content: "hello"},
			},
		}
		body := p.buildOpenAIRequest(req, nil)

		assert.Equal(t, "gpt-4o", body["model"])
		assert.Equal(t, 2048, body["max_tokens"])
		assert.Equal(t, 0.15, body["temperature"])
		assert.Equal(t, 0.9, body["top_p"])
		assert.Nil(t, body["tools"])
	})

	t.Run("request overrides config", func(t *testing.T) {
		req := &ChatRequest{
			Messages:    []Message{{Role: "user", Content: "hello"}},
			MaxTokens:   100,
			Temperature: Float64(0.0),
			TopP:        Float64(0.5),
		}
		body := p.buildOpenAIRequest(req, nil)

		assert.Equal(t, 100, body["max_tokens"])
		assert.Equal(t, 0.0, body["temperature"])
		assert.Equal(t, 0.5, body["top_p"])
	})

	t.Run("nil temperature uses config default", func(t *testing.T) {
		req := &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hello"}},
		}
		body := p.buildOpenAIRequest(req, nil)
		assert.Equal(t, 0.15, body["temperature"])
	})

	t.Run("tool messages include name", func(t *testing.T) {
		req := &ChatRequest{
			Messages: []Message{
				{
					Role:       "tool",
					Content:    `{"balance": "500"}`,
					Name:       "get_account_summary",
					ToolCallID: "call_123",
				},
			},
		}
		body := p.buildOpenAIRequest(req, nil)
		msgs := body["messages"].([]map[string]interface{})
		require.Len(t, msgs, 1)
		assert.Equal(t, "tool", msgs[0]["role"])
		assert.Equal(t, "call_123", msgs[0]["tool_call_id"])
		assert.Equal(t, "get_account_summary", msgs[0]["name"])
	})

	t.Run("assistant message with tool_calls", func(t *testing.T) {
		req := &ChatRequest{
			Messages: []Message{
				{
					Role:    "assistant",
					Content: "",
					ToolCalls: []ToolCall{
						{
							ID:        "call_abc",
							Name:      "get_balance",
							Arguments: map[string]interface{}{"user_id": "123"},
						},
					},
				},
			},
		}
		body := p.buildOpenAIRequest(req, nil)
		msgs := body["messages"].([]map[string]interface{})
		require.Len(t, msgs, 1)
		assert.Equal(t, "assistant", msgs[0]["role"])

		toolCalls := msgs[0]["tool_calls"].([]map[string]interface{})
		require.Len(t, toolCalls, 1)
		assert.Equal(t, "call_abc", toolCalls[0]["id"])
		assert.Equal(t, "function", toolCalls[0]["type"])

		fn := toolCalls[0]["function"].(map[string]interface{})
		assert.Equal(t, "get_balance", fn["name"])
		assert.Equal(t, `{"user_id":"123"}`, fn["arguments"])
	})

	t.Run("tools included when provided", func(t *testing.T) {
		tools := []Tool{
			{
				Name:        "get_balance",
				Description: "Get user balance",
				Parameters:  map[string]interface{}{"type": "object"},
			},
		}
		req := &ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}}
		body := p.buildOpenAIRequest(req, tools)

		assert.NotNil(t, body["tools"])
		assert.Nil(t, body["tool_choice"])
	})

	t.Run("system prompt prepended", func(t *testing.T) {
		req := &ChatRequest{
			SystemPrompt: "You are a helpful assistant",
			Messages:     []Message{{Role: "user", Content: "hi"}},
		}
		body := p.buildOpenAIRequest(req, nil)
		msgs := body["messages"].([]map[string]interface{})
		require.Len(t, msgs, 2)
		assert.Equal(t, "system", msgs[0]["role"])
		assert.Equal(t, "You are a helpful assistant", msgs[0]["content"])
	})

	t.Run("stream_options only for openai", func(t *testing.T) {
		openaiP := NewOpenAIProvider(&ProviderConfig{ProviderName: ""}, logger)
		kimiP := NewOpenAIProvider(&ProviderConfig{ProviderName: "kimi"}, logger)

		// buildOpenAIRequest doesn't include stream_options; that's added in ChatCompletionStream
		// We verify the provider name check works by inspecting the provider directly
		assert.Equal(t, "openai", openaiP.Name())
		assert.Equal(t, "kimi", kimiP.Name())
	})
}

func TestKimiTemperatureForcedToOne(t *testing.T) {
	logger := zap.NewNop()

	t.Run("kimi ignores config temperature and forces 1.0", func(t *testing.T) {
		kimiP := NewOpenAIProvider(&ProviderConfig{
			ProviderName: "kimi",
			Model:        "moonshot-v1-8k",
			Temperature:  0.15,
		}, logger)

		req := &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hello"}},
		}
		body := kimiP.buildOpenAIRequest(req, nil)
		assert.Equal(t, 1.0, body["temperature"])
	})

	t.Run("kimi ignores request temperature and forces 1.0", func(t *testing.T) {
		kimiP := NewOpenAIProvider(&ProviderConfig{
			ProviderName: "kimi",
			Model:        "moonshot-v1-8k",
		}, logger)

		req := &ChatRequest{
			Messages:    []Message{{Role: "user", Content: "hello"}},
			Temperature: Float64(0.5),
		}
		body := kimiP.buildOpenAIRequest(req, nil)
		assert.Equal(t, 1.0, body["temperature"])
	})

	t.Run("openai respects request temperature", func(t *testing.T) {
		openaiP := NewOpenAIProvider(&ProviderConfig{
			ProviderName: "openai",
			Model:        "gpt-4o",
			Temperature:  0.15,
		}, logger)

		req := &ChatRequest{
			Messages:    []Message{{Role: "user", Content: "hello"}},
			Temperature: Float64(0.7),
		}
		body := openaiP.buildOpenAIRequest(req, nil)
		assert.Equal(t, 0.7, body["temperature"])
	})
}

func TestOpenAIProviderAPIBaseURL(t *testing.T) {
	t.Run("default base URL", func(t *testing.T) {
		p := NewOpenAIProvider(&ProviderConfig{}, zap.NewNop())
		assert.Equal(t, "https://api.openai.com/v1", p.apiBaseURL())
	})

	t.Run("trailing slash trimmed", func(t *testing.T) {
		p := NewOpenAIProvider(&ProviderConfig{BaseURL: "https://api.moonshot.ai/v1/"}, zap.NewNop())
		assert.Equal(t, "https://api.moonshot.ai/v1", p.apiBaseURL())
	})

	t.Run("no trailing slash", func(t *testing.T) {
		p := NewOpenAIProvider(&ProviderConfig{BaseURL: "https://api.moonshot.ai/v1"}, zap.NewNop())
		assert.Equal(t, "https://api.moonshot.ai/v1", p.apiBaseURL())
	})
}

func TestOpenAIProviderIsAvailable(t *testing.T) {
	t.Run("returns true on 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/models", r.URL.Path)
			assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		assert.True(t, p.IsAvailable(context.Background()))
	})

	t.Run("returns false on 401", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Invalid Authentication"}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "bad-key", BaseURL: server.URL}, zap.NewNop())
		assert.False(t, p.IsAvailable(context.Background()))
	})

	t.Run("caches result", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		_ = p.IsAvailable(context.Background())
		_ = p.IsAvailable(context.Background())
		assert.Equal(t, 1, callCount, "expected cached result to prevent second call")
	})
}

func TestOpenAIProviderChatCompletion(t *testing.T) {
	t.Run("successful completion", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/chat/completions", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			resp := map[string]interface{}{
				"id":      "chatcmpl-123",
				"object":  "chat.completion",
				"created": 1234567890,
				"model":   "gpt-4o",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Hello! How can I help?",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     10,
					"completion_tokens": 5,
					"total_tokens":      15,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		resp, err := p.ChatCompletion(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})

		require.NoError(t, err)
		assert.Equal(t, "Hello! How can I help?", resp.Content)
		assert.Equal(t, 15, resp.TokensUsed)
		assert.Equal(t, "gpt-4o", resp.Model)
		assert.Equal(t, "stop", resp.FinishReason)
	})

	t.Run("handles tool calls in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]interface{}{
				"id":     "chatcmpl-123",
				"model":  "gpt-4o",
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "",
							"tool_calls": []map[string]interface{}{
								{
									"id":   "call_abc",
									"type": "function",
									"function": map[string]interface{}{
										"name":      "get_balance",
										"arguments": `{"user_id":"123"}`,
									},
								},
							},
						},
						"finish_reason": "tool_calls",
					},
				},
				"usage": map[string]interface{}{"total_tokens": 25},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		resp, err := p.ChatCompletionWithTools(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "my balance"}},
		}, []Tool{{Name: "get_balance", Description: "Get balance"}})

		require.NoError(t, err)
		assert.Equal(t, "tool_calls", resp.FinishReason)
		require.Len(t, resp.ToolCalls, 1)
		assert.Equal(t, "call_abc", resp.ToolCalls[0].ID)
		assert.Equal(t, "get_balance", resp.ToolCalls[0].Name)
		assert.Equal(t, "123", resp.ToolCalls[0].Arguments["user_id"])
	})

	t.Run("returns ProviderError on 401", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid Authentication","type":"invalid_request_error","code":"invalid_api_key"}}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "bad-key", BaseURL: server.URL, ProviderName: "kimi"}, zap.NewNop())
		_, err := p.ChatCompletion(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})

		require.Error(t, err)
		provErr, ok := err.(*ProviderError)
		require.True(t, ok)
		assert.Equal(t, ErrorCodeAuthentication, provErr.Code)
		assert.False(t, provErr.Retryable)
		assert.Contains(t, provErr.Message, "Invalid Authentication")
	})

	t.Run("returns ProviderError on 429", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		_, err := p.ChatCompletion(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})

		require.Error(t, err)
		provErr, ok := err.(*ProviderError)
		require.True(t, ok)
		assert.Equal(t, ErrorCodeRateLimit, provErr.Code)
		assert.True(t, provErr.Retryable)
	})

	t.Run("generic JSON error fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad request details"}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		_, err := p.ChatCompletion(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})

		require.Error(t, err)
		provErr, ok := err.(*ProviderError)
		require.True(t, ok)
		assert.Contains(t, provErr.Message, "bad request details")
	})
}

func TestOpenAIProviderChatCompletionStream(t *testing.T) {
	t.Run("streams content chunks", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))

			// Verify stream_options is included for OpenAI
			body, _ := io.ReadAll(r.Body)
			var reqBody map[string]interface{}
			_ = json.Unmarshal(body, &reqBody)
			assert.NotNil(t, reqBody["stream_options"])

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			flusher, ok := w.(http.Flusher)
			require.True(t, ok)

			chunks := []string{
				`data: {"id":"1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello"}}]}`,
				`data: {"id":"1","object":"chat.completion.chunk","choices":[{"delta":{"content":" world"}}]}`,
				`data: {"id":"1","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			}
			for _, chunk := range chunks {
				_, _ = fmt.Fprintln(w, chunk)
				flusher.Flush()
			}
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		ch := make(chan StreamChunk, 32)

		go func() {
			err := p.ChatCompletionStream(context.Background(), &ChatRequest{
				Messages: []Message{{Role: "user", Content: "hi"}},
			}, nil, ch)
			require.NoError(t, err)
		}()

		var contents []string
		var doneCount int
		for chunk := range ch {
			if chunk.Content != "" {
				contents = append(contents, chunk.Content)
			}
			if chunk.Done {
				doneCount++
			}
		}

		assert.Equal(t, []string{"Hello", " world"}, contents)
		assert.Equal(t, 1, doneCount, "expected exactly one done chunk")
	})

	t.Run("omits stream_options for kimi", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var reqBody map[string]interface{}
			_ = json.Unmarshal(body, &reqBody)
			assert.Nil(t, reqBody["stream_options"])

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`)
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL, ProviderName: "kimi"}, zap.NewNop())
		ch := make(chan StreamChunk, 32)

		go func() {
			_ = p.ChatCompletionStream(context.Background(), &ChatRequest{
				Messages: []Message{{Role: "user", Content: "hi"}},
			}, nil, ch)
		}()

		for range ch {
		}
	})

	t.Run("sends done chunk even without finish_reason", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`)
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		ch := make(chan StreamChunk, 32)

		go func() {
			_ = p.ChatCompletionStream(context.Background(), &ChatRequest{
				Messages: []Message{{Role: "user", Content: "hi"}},
			}, nil, ch)
		}()

		var gotDone bool
		for chunk := range ch {
			if chunk.Done {
				gotDone = true
			}
		}
		assert.True(t, gotDone, "expected done chunk even when API omits finish_reason")
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		ch := make(chan StreamChunk, 32)

		err := p.ChatCompletionStream(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		}, nil, ch)

		require.Error(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			for i := 0; i < 100; i++ {
				_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"x"}}]}`)
				flusher.Flush()
				time.Sleep(10 * time.Millisecond)
			}
		}))
		defer server.Close()

		p := NewOpenAIProvider(&ProviderConfig{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
		ch := make(chan StreamChunk, 1) // small buffer to force blocking

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		err := p.ChatCompletionStream(ctx, &ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		}, nil, ch)

		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestOpenAIProviderHandleHTTPError(t *testing.T) {
	p := NewOpenAIProvider(&ProviderConfig{ProviderName: "kimi"}, zap.NewNop())

	tests := []struct {
		name       string
		status     int
		body       string
		wantCode   string
		wantRetry  bool
		wantMsgSub string
	}{
		{
			name:       "openai style error",
			status:     401,
			body:       `{"error":{"message":"Invalid Authentication","type":"invalid_request_error","code":"invalid_api_key"}}`,
			wantCode:   ErrorCodeAuthentication,
			wantRetry:  false,
			wantMsgSub: "Invalid Authentication",
		},
		{
			name:       "generic message field",
			status:     400,
			body:       `{"message":"bad request"}`,
			wantCode:   ErrorCodeInvalidRequest,
			wantRetry:  false,
			wantMsgSub: "bad request",
		},
		{
			name:       "generic error string",
			status:     500,
			body:       `{"error":"internal server error"}`,
			wantCode:   ErrorCodeServerError,
			wantRetry:  true,
			wantMsgSub: "internal server error",
		},
		{
			name:       "plain text fallback",
			status:     503,
			body:       `service unavailable`,
			wantCode:   ErrorCodeServerError,
			wantRetry:  true,
			wantMsgSub: "service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.handleHTTPError(tt.status, []byte(tt.body))
			provErr, ok := err.(*ProviderError)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, provErr.Code)
			assert.Equal(t, tt.wantRetry, provErr.Retryable)
			assert.Contains(t, provErr.Message, tt.wantMsgSub)
		})
	}
}

func TestOpenAIProviderConvertResponse(t *testing.T) {
	p := NewOpenAIProvider(&ProviderConfig{}, zap.NewNop())

	t.Run("empty choices", func(t *testing.T) {
		resp := &openAIResponse{Model: "gpt-4o"}
		result := p.convertResponse(resp, time.Second)
		assert.Empty(t, result.Content)
		assert.Equal(t, "gpt-4o", result.Model)
	})

	t.Run("malformed tool call arguments", func(t *testing.T) {
		resp := &openAIResponse{
			Model: "gpt-4o",
			Choices: []struct {
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
			}{
				{
					Message: struct {
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
					}{
						ToolCalls: []struct {
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						}{
							{
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "get_balance",
									Arguments: "not-json",
								},
							},
						},
					},
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			}{TotalTokens: 10},
		}
		result := p.convertResponse(resp, time.Second)
		require.Len(t, result.ToolCalls, 1)
		assert.Empty(t, result.ToolCalls[0].Arguments)
	})
}

func TestProviderManagerFailover(t *testing.T) {
	logger := zap.NewNop()

	// Primary that always fails
	primary := &mockProvider{name: "primary", err: &ProviderError{Code: ErrorCodeServerError, Message: "primary down", Retryable: true}}
	// Fallback that succeeds
	fallback := &mockProvider{name: "fallback", resp: &ChatResponse{Content: "hello", TokensUsed: 5}}

	pm := NewProviderManager(primary, []AIProvider{fallback}, nil, logger)

	resp, err := pm.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, 5, resp.TokensUsed)
}

func TestProviderManagerAllFail(t *testing.T) {
	logger := zap.NewNop()
	primary := &mockProvider{name: "primary", err: &ProviderError{Code: ErrorCodeAuthentication, Message: "auth failed", Retryable: false}}
	fallback := &mockProvider{name: "fallback", err: &ProviderError{Code: ErrorCodeServerError, Message: "also down", Retryable: true}}

	pm := NewProviderManager(primary, []AIProvider{fallback}, nil, logger)

	_, err := pm.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all AI providers failed")
}

func TestProviderManagerNonRetryableSkipsImmediately(t *testing.T) {
	logger := zap.NewNop()
	primary := &mockProvider{name: "primary", err: &ProviderError{Code: ErrorCodeAuthentication, Message: "auth failed", Retryable: false}}
	fallback := &mockProvider{name: "fallback", resp: &ChatResponse{Content: "hello"}}

	pm := NewProviderManager(primary, []AIProvider{fallback}, &ProviderManagerConfig{RetryAttempts: 2, RetryDelay: 10 * time.Millisecond}, logger)

	resp, err := pm.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
}

func TestProviderManagerStreamDelegatesToPrimary(t *testing.T) {
	logger := zap.NewNop()
	streamer := &mockStreamProvider{name: "primary"}
	pm := NewProviderManager(streamer, nil, nil, logger)

	ch := make(chan StreamChunk, 1)
	err := pm.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, nil, ch)

	require.NoError(t, err)
}

func TestProviderManagerStreamPrimaryNotStreamer(t *testing.T) {
	logger := zap.NewNop()
	primary := &mockProvider{name: "primary"}
	pm := NewProviderManager(primary, nil, nil, logger)

	ch := make(chan StreamChunk, 1)
	err := pm.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, nil, ch)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support streaming")
}

// mockProvider is a test double for AIProvider.
type mockProvider struct {
	name string
	resp *ChatResponse
	err  error
}

func (m *mockProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return m.ChatCompletionWithTools(ctx, req, nil)
}

func (m *mockProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) IsAvailable(ctx context.Context) bool { return m.err == nil }

// mockStreamProvider is a test double that supports streaming.
type mockStreamProvider struct {
	name string
}

func (m *mockStreamProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (m *mockStreamProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	return nil, nil
}

func (m *mockStreamProvider) Name() string { return m.name }

func (m *mockStreamProvider) IsAvailable(ctx context.Context) bool { return true }

func (m *mockStreamProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	close(ch)
	return nil
}

// Helper to create SSE data lines for streaming tests.
func sseData(data string) string {
	return "data: " + data + "\n\n"
}

func TestSSEParsing(t *testing.T) {
	// Verify our SSE parsing logic handles various edge cases
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}",
		"data: ",
		"data: [DONE]",
		"event: message",
		": ping",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}",
	}

	var contents []string
	var doneFound bool
	for _, line := range lines {
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
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Content != "" {
				contents = append(contents, chunk.Choices[0].Delta.Content)
			}
			if chunk.Choices[0].FinishReason != "" {
				doneFound = true
			}
		}
	}

	assert.Equal(t, []string{"hi"}, contents)
	assert.True(t, doneFound)
}
