package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// StreamEvent is sent to the client via SSE.
type StreamEvent struct {
	Type    string      `json:"type"`              // "token", "tool_result", "cards", "done", "error"
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ChatStream streams a chat response via SSE. Tool calls are executed
// non-streaming (up to 3 rounds), then the final answer is streamed.
func (o *Orchestrator) ChatStream(ctx context.Context, userID uuid.UUID, message string, history []infraai.Message, emit func(StreamEvent)) error {
	start := time.Now()

	streamer, ok := o.aiProvider.(infraai.StreamProvider)
	if !ok {
		// Fallback: non-streaming
		resp, err := o.Chat(ctx, userID, message, history)
		if err != nil {
			return err
		}
		emit(StreamEvent{Type: "token", Content: resp.Content})
		if len(resp.Cards) > 0 {
			emit(StreamEvent{Type: "cards", Data: resp.Cards})
		}
		emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": resp.TokensUsed, "provider": resp.Provider}})
		return nil
	}

	messages := make([]infraai.Message, len(history), len(history)+8)
	copy(messages, history)
	messages = append(messages, infraai.Message{Role: "user", Content: message})

	req := &infraai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    500,
		Temperature:  0.7,
	}

	// Non-streaming tool call rounds (up to 3)
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
	if err != nil {
		observeChat("unknown", time.Since(start), 0, err)
		return fmt.Errorf("AI completion failed: %w", err)
	}

	allToolResults := make([]ToolResult, 0)
	for round := 0; round < 3 && len(resp.ToolCalls) > 0; round++ {
		roundResults := make([]ToolResult, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			result, execErr := o.executeTool(ctx, userID, tc)
			observeToolCall(tc.Name, execErr)
			if execErr != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(execErr))
				result = map[string]interface{}{"error": execErr.Error()}
			}
			roundResults = append(roundResults, ToolResult{Name: tc.Name, Result: result})
			allToolResults = append(allToolResults, ToolResult{Name: tc.Name, Result: result})
			emit(StreamEvent{Type: "tool_result", Data: map[string]interface{}{"tool": tc.Name}})
		}

		toolResultsJSON, _ := json.Marshal(roundResults)
		assistantContent := resp.Content
		if assistantContent == "" {
			assistantContent = "Calling tools..."
		}
		messages = append(messages, infraai.Message{Role: "assistant", Content: assistantContent})
		messages = append(messages, infraai.Message{Role: "user", Content: fmt.Sprintf("Tool results: %s", string(toolResultsJSON))})
		req.Messages = messages

		resp, err = o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
		if err != nil {
			return fmt.Errorf("follow-up completion failed: %w", err)
		}
	}

	// Emit cards from tool results
	cards := buildCardsFromToolResults(allToolResults)
	if len(cards) > 0 {
		emit(StreamEvent{Type: "cards", Data: cards})
	}

	// If no more tool calls, emit the final answer
	if len(resp.ToolCalls) == 0 {
		if resp.Content != "" {
			content := o.applySafetyFilter(resp.Content)
			emit(StreamEvent{Type: "token", Content: content})
		}
		observeChat(resp.Provider, time.Since(start), resp.TokensUsed, nil)
		emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": resp.TokensUsed, "provider": resp.Provider}})
		return nil
	}

	// Stream follow-up
	ch := make(chan infraai.StreamChunk, 32)
	errCh := make(chan error, 1)
	go func() {
		errCh <- streamer.ChatCompletionStream(ctx, req, nil, ch)
	}()

	var totalTokens int
	provider := resp.Provider
	for chunk := range ch {
		if chunk.Content != "" {
			emit(StreamEvent{Type: "token", Content: chunk.Content})
		}
		if chunk.Done {
			totalTokens = chunk.TokensUsed
		}
	}

	if streamErr := <-errCh; streamErr != nil {
		observeChat(provider, time.Since(start), 0, streamErr)
		return streamErr
	}

	observeChat(provider, time.Since(start), totalTokens, nil)
	emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": totalTokens, "provider": provider}})
	return nil
}
