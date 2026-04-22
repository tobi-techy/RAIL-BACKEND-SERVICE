package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// StreamEvent is sent to the client via SSE.
type StreamEvent struct {
	Type    string      `json:"type"` // "token", "tool_result", "cards", "done", "error"
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ChatStream streams a chat response via SSE. Tool calls are executed
// non-streaming (up to 3 rounds), then the final answer is streamed.
func (o *Orchestrator) ChatStream(ctx context.Context, userID uuid.UUID, message string, history []infraai.Message, emit func(StreamEvent)) error {
	return o.chatStreamInternal(ctx, userID, uuid.Nil, message, history, emit)
}

// ChatStreamInConversation streams a chat response within a persisted conversation.
func (o *Orchestrator) ChatStreamInConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, emit func(StreamEvent)) error {
	var history []infraai.Message
	if o.conversations != nil {
		var err error
		history, err = o.conversations.BuildContext(ctx, conv)
		if err != nil {
			o.logger.Warn("failed to build conversation context for stream", zap.Error(err))
		}
	}

	var accumulated strings.Builder
	var totalTokens int
	var modelUsed string
	var providerUsed string
	var streamCards []entities.InsightCard
	wrappedEmit := func(event StreamEvent) {
		if event.Type == "token" {
			accumulated.WriteString(event.Content)
		}
		if event.Type == "done" {
			if m, ok := event.Data.(map[string]interface{}); ok {
				switch t := m["tokens_used"].(type) {
				case int:
					totalTokens = t
				case float64:
					totalTokens = int(t)
				}
				if model, ok := m["model"].(string); ok {
					modelUsed = model
				}
				if provider, ok := m["provider"].(string); ok {
					providerUsed = provider
				}
			}
		}
		if event.Type == "cards" {
			if c, ok := event.Data.([]entities.InsightCard); ok {
				streamCards = c
			}
		}
		emit(event)
	}

	err := o.chatStreamInternal(ctx, userID, conv.ID, message, history, wrappedEmit)

	// Persist exchange in background (best-effort, mirrors ChatWithConversation)
	if o.conversations != nil {
		content := accumulated.String()
		tokens := totalTokens
		model := modelUsed
		if model == "" {
			model = providerUsed
		}
		cost := entities.EstimateCost(model, tokens)
		cards := streamCards
		go func() {
			pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if persistErr := o.conversations.RecordExchange(pCtx, conv.ID, message, content, tokens, cost, model, cards); persistErr != nil {
				o.logger.Error("failed to persist streamed chat exchange",
					zap.Error(persistErr),
					zap.String("conversation_id", conv.ID.String()),
				)
			}
			if o.usage != nil && tokens > 0 {
				if trackErr := o.usage.TrackInteraction(pCtx, userID, model, tokens); trackErr != nil {
					o.logger.Error("failed to track streamed chat usage", zap.Error(trackErr))
				}
			}
		}()
	}

	return err
}

func (o *Orchestrator) chatStreamInternal(ctx context.Context, userID, convID uuid.UUID, message string, history []infraai.Message, emit func(StreamEvent)) error {
	start := time.Now()

	streamer, ok := o.aiProvider.(infraai.StreamProvider)
	if !ok {
		// Fallback: non-streaming
		resp, err := o.ChatInContext(ctx, userID, convID, message, history)
		if err != nil {
			return err
		}
		emit(StreamEvent{Type: "token", Content: resp.Content})
		if len(resp.Cards) > 0 {
			emit(StreamEvent{Type: "cards", Data: resp.Cards})
		}
		if resp.PendingAction != nil {
			emit(StreamEvent{Type: "pending_action", Data: resp.PendingAction})
		}
		emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": resp.TokensUsed, "provider": resp.Provider, "model": resp.Provider}})
		return nil
	}

	messages := make([]infraai.Message, len(history), len(history)+10)
	copy(messages, history)

	// Inject current balance snapshot so the LLM always knows the user's financial position
	if balanceCtx := o.buildBalanceContext(ctx, userID); balanceCtx != "" {
		messages = append(messages, infraai.Message{Role: "system", Content: balanceCtx})
	}

	messages = append(messages, infraai.Message{Role: "user", Content: message})

	req := &infraai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    2048,
		Temperature:  0.15,
	}

	// Non-streaming tool call rounds (up to 5)
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
	if err != nil {
		observeChat("unknown", time.Since(start), 0, err)
		return fmt.Errorf("AI completion failed: %w", err)
	}

	cumulativeTokens := resp.TokensUsed
	allToolResults := make([]ToolResult, 0)
	for round := 0; round < 5 && len(resp.ToolCalls) > 0; round++ {
		roundResults := make([]ToolResult, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			// Handle action tools (require confirmation)
			if isActionTool(tc.Name) && convID != uuid.Nil && (o.fundsTransferer != nil || tc.Name == ToolSplitReceipt) {
				result, execErr := o.executeActionTool(ctx, userID, convID, tc)
				observeToolCall(tc.Name, execErr)
				if execErr != nil {
					result = o.sanitizeToolError(tc.Name, execErr)
				}
				if actionRequired, _ := result["action_required"].(bool); actionRequired {
					pendingRaw, _ := result["pending_action"].(*entities.PendingAction)
					modelName := resp.Model
					if modelName == "" {
						modelName = resp.Provider
					}
					emit(StreamEvent{Type: "pending_action", Data: pendingRaw})
					emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": cumulativeTokens, "provider": resp.Provider, "model": modelName}})
					observeChat(resp.Provider, time.Since(start), cumulativeTokens, nil)
					return nil
				}
				roundResults = append(roundResults, ToolResult{Name: tc.Name, Result: result})
				allToolResults = append(allToolResults, ToolResult{Name: tc.Name, Result: result})
				emit(StreamEvent{Type: "tool_result", Data: map[string]interface{}{"tool": tc.Name}})
				continue
			}

			result, execErr := o.executeTool(ctx, userID, tc)
			observeToolCall(tc.Name, execErr)
			if execErr != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(execErr))
				result = o.sanitizeToolError(tc.Name, execErr)
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
		messages = append(messages, infraai.Message{Role: "tool", Content: string(toolResultsJSON)})
		req.Messages = messages

		resp, err = o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
		if err != nil {
			return fmt.Errorf("follow-up completion failed: %w", err)
		}
		cumulativeTokens += resp.TokensUsed
	}

	// Emit cards from tool results
	cards := buildCardsFromToolResults(allToolResults)
	if len(cards) > 0 {
		emit(StreamEvent{Type: "cards", Data: cards})
	}

	// If no more tool calls, stream the final answer
	if len(resp.ToolCalls) == 0 {
		if resp.Content != "" {
			content := o.applySafetyFilter(resp.Content)
			emit(StreamEvent{Type: "token", Content: content})
		}
		modelName := resp.Model
		if modelName == "" {
			modelName = resp.Provider
		}
		observeChat(resp.Provider, time.Since(start), cumulativeTokens, nil)
		emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": cumulativeTokens, "provider": resp.Provider, "model": modelName}})
		return nil
	}

	// Stream follow-up — collect full content for safety filter
	ch := make(chan infraai.StreamChunk, 32)
	errCh := make(chan error, 1)
	go func() {
		errCh <- streamer.ChatCompletionStream(ctx, req, nil, ch)
	}()

	var streamedContent strings.Builder
	var streamTokens int
	provider := resp.Provider
	model := resp.Model
	if model == "" {
		model = provider
	}
	for chunk := range ch {
		if chunk.Content != "" {
			streamedContent.WriteString(chunk.Content)
			emit(StreamEvent{Type: "token", Content: chunk.Content})
		}
		if chunk.Done {
			streamTokens = chunk.TokensUsed
		}
	}

	// Apply safety filter to the fully assembled streamed content.
	// If triggered, emit the disclaimer as a final token.
	fullContent := streamedContent.String()
	filtered := o.applySafetyFilter(fullContent)
	if len(filtered) > len(fullContent) {
		emit(StreamEvent{Type: "token", Content: filtered[len(fullContent):]})
	}

	cumulativeTokens += streamTokens

	if streamErr := <-errCh; streamErr != nil {
		observeChat(provider, time.Since(start), 0, streamErr)
		return streamErr
	}

	observeChat(provider, time.Since(start), cumulativeTokens, nil)
	emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": cumulativeTokens, "provider": provider, "model": model}})
	return nil
}
