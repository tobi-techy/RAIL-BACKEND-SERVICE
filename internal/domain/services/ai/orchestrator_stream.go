package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/tracing"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
func (o *Orchestrator) ChatStream(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, emit func(StreamEvent)) error {
	return o.ChatStreamWithOptions(ctx, userID, message, history, ChatOptions{}, emit)
}

func (o *Orchestrator) ChatStreamWithOptions(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, opts ChatOptions, emit func(StreamEvent)) error {
	return o.chatStreamInternal(ctx, userID, uuid.Nil, message, history, opts, emit)
}

// ChatStreamInConversation streams a chat response within a persisted conversation.
func (o *Orchestrator) ChatStreamInConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, emit func(StreamEvent)) error {
	return o.ChatStreamInConversationWithOptions(ctx, userID, conv, message, ChatOptions{}, emit)
}

func (o *Orchestrator) ChatStreamInConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions, emit func(StreamEvent)) error {
	var history []ai.Message
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

	toolsUsed := make([]string, 0)
	originalEmit := wrappedEmit
	wrappedEmit = func(event StreamEvent) {
		if event.Type == "tool_result" {
			if m, ok := event.Data.(map[string]interface{}); ok {
				if toolName, ok := m["tool"].(string); ok {
					toolsUsed = append(toolsUsed, toolName)
				}
			}
		}
		originalEmit(event)
	}

	err := o.chatStreamInternal(ctx, userID, conv.ID, message, history, opts, wrappedEmit)

	// Track conversation completion
	if err == nil {
		analytics.TrackEvent(ctx, userID.String(), analytics.EventAIConversationCompleted, map[string]any{
			"conversation_id": conv.ID.String(),
			"tokens_used":     totalTokens,
			"tools_used":      toolsUsed,
			"tools_count":     len(toolsUsed),
			"model":           modelUsed,
			"message_length":  len(message),
			"response_length": len(accumulated.String()),
			"had_tool_calls":  len(toolsUsed) > 0,
			"had_cards":       len(streamCards) > 0,
		})
	}

	// Persist exchange in background (best-effort, mirrors ChatWithConversation)
	content := accumulated.String()
	if o.conversations != nil && err == nil && strings.TrimSpace(content) != "" {
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
			// Extract memorable moments for future callbacks
			if o.memory != nil {
				o.memory.ExtractMoment(userID, message, content)
			}
		}()
	}

	return err
}

func (o *Orchestrator) chatStreamInternal(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message, opts ChatOptions, emit func(StreamEvent)) error {
	// Trivial message bypass — skip LLM entirely for greetings/acknowledgements
	if reply := trivialReply(message); reply != "" {
		emitWithBubbleBreaks(reply, emit)
		emit(StreamEvent{Type: "done", Data: map[string]interface{}{"tokens_used": 0, "provider": "bypass", "model": "bypass"}})
		return nil
	}

	// Root trace span for this turn — groups generations into a Langfuse
	// session (per conversation) and attributes them to the user. The marker is
	// a start-time attribute so llmAwareSampler force-samples it (and, via the
	// child generations' own markers, the whole turn) at 100%.
	ctx, span := otel.Tracer("miriam.chat").Start(ctx, "miriam.chat",
		trace.WithAttributes(attribute.String(tracing.LangfuseObservationTypeKey, "span")))
	defer span.End()
	span.SetAttributes(
		attribute.String("langfuse.user.id", userID.String()),
		attribute.StringSlice("langfuse.tags", []string{"miriam", "chat"}),
	)
	if convID != uuid.Nil {
		span.SetAttributes(attribute.String("langfuse.session.id", convID.String()))
	}

	start := time.Now()

	analytics.TrackEvent(ctx, userID.String(), analytics.EventAIQuestionAsked, map[string]any{
		"message_length":  len(message),
		"conversation_id": convID.String(),
		"history_count":   len(history),
	})

	streamer, ok := o.aiProvider.(ai.StreamProvider)
	if !ok {
		// Fallback: non-streaming
		resp, err := o.ChatInContextWithOptions(ctx, userID, convID, message, history, opts)
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

	messages := make([]ai.Message, len(history), len(history)+10)
	copy(messages, history)

	// Assemble all context in parallel (3s ceiling)
	messages = append(messages, o.assembleContext(ctx, userID, ContextAssemblyOpts{
		ToneMode: opts.ToneMode,
		Message:  message,
	})...)

	// Tool usage rules — skip for very short casual messages to save tokens
	if len(message) > 15 || classifyMessage(message) != CategoryFull {
		messages = append(messages, ai.Message{Role: "system", Content: SystemPromptTools})
	}
	messages = append(messages, ai.Message{Role: "user", Content: message})

	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPromptV2,
		MaxTokens:    2048,
		Temperature:  ai.Float64(0.6),
		ModelHint:    classifyQueryComplexity(message),
	}

	// Non-streaming tool call rounds (up to 5)
	tools := o.RouteTools(message)
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, tools)
	if err != nil {
		observeChat("unknown", time.Since(start), 0, err)
		return fmt.Errorf("AI completion failed: %w", err)
	}

	cumulativeTokens := resp.TokensUsed
	allToolResults := make([]ToolResult, 0)
	// Use a detached context for tool execution so side-effects complete
	// even if the client disconnects mid-stream. This is safe because:
	// - Action tools are capped at $500 and require account status checks.
	// - The 30s timeout bounds total execution time.
	// - Read-only tools are idempotent.
	toolCtx, toolCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer toolCancel()
	for round := 0; round < 5 && len(resp.ToolCalls) > 0; round++ {
		roundResults := make([]ToolResult, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			emit(StreamEvent{Type: "thinking", Content: thinkingMessage(tc.Name)})
			// Handle action tools (require confirmation)
			if isActionTool(tc.Name) && convID != uuid.Nil && o.canCreateActionTool(tc.Name) {
				result, execErr := o.executeActionTool(toolCtx, userID, convID, tc)
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

			result, execErr := o.executeTool(toolCtx, userID, tc)
			observeToolCall(tc.Name, execErr)
			if execErr != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(execErr))
				result = o.sanitizeToolError(tc.Name, execErr)
			}
			roundResults = append(roundResults, ToolResult{Name: tc.Name, Result: result})
			allToolResults = append(allToolResults, ToolResult{Name: tc.Name, Result: result})
			emit(StreamEvent{Type: "tool_result", Data: map[string]interface{}{"tool": tc.Name}})
			// Surface any card-ready directive (e.g. web_search places/flights)
			// so the chat client can render it on the Miriam Canvas, same as voice.
			if disp, ok := result["display"]; ok && disp != nil {
				emit(StreamEvent{Type: "ui_directive", Data: disp})
			}
		}

		// Build assistant message with tool_calls preserved
		assistantContent := resp.Content
		if assistantContent == "" {
			assistantContent = "Calling tools..."
		}
		assistantMsg := ai.Message{
			Role:             "assistant",
			Content:          assistantContent,
			ToolCalls:        resp.ToolCalls,
			ReasoningContent: resp.ReasoningContent,
		}
		messages = append(messages, assistantMsg)

		// Append each tool result with its corresponding tool_call_id.
		// roundResults[i] maps 1:1 to resp.ToolCalls[i] by construction.
		for i, tr := range roundResults {
			resultJSON, _ := json.Marshal(tr.Result)
			toolCallID := ""
			if i < len(resp.ToolCalls) {
				toolCallID = resp.ToolCalls[i].ID
			}
			messages = append(messages, ai.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				Name:       tr.Name,
				ToolCallID: toolCallID,
			})
		}
		// Increase MaxTokens for follow-up completions when heavy tools produce large payloads
		maxFollowUp := 2048
		for _, tr := range roundResults {
			if tr.Name == ToolGetFinancialAudit || tr.Name == ToolGetFinancialHealth {
				maxFollowUp = 4096
				break
			}
		}
		req.MaxTokens = maxFollowUp
		req.Messages = messages

		// Keep the follow-up on the provider that produced the tool calls. Tool-call
		// IDs are provider-specific, so failing over to a different provider mid-round
		// can trigger "tool_call_id is not found"; it also avoids re-hammering providers
		// that were already rate-limited on the first round.
		prevProvider := resp.Provider
		if pp, ok := o.aiProvider.(interface {
			ChatCompletionWithToolsPreferring(context.Context, *ai.ChatRequest, []ai.Tool, string) (*ai.ChatResponse, error)
		}); ok && prevProvider != "" {
			resp, err = pp.ChatCompletionWithToolsPreferring(ctx, req, tools, prevProvider)
		} else {
			resp, err = o.aiProvider.ChatCompletionWithTools(ctx, req, tools)
		}
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

	// Emit action chips from tool results
	if chips := buildActionChipsFromToolResults(allToolResults); len(chips) > 0 {
		emit(StreamEvent{Type: "action_chips", Data: chips})
	}

	// If no more tool calls, stream the final answer
	if len(resp.ToolCalls) == 0 {
		if resp.Content != "" {
			content := o.applySafetyFilter(resp.Content)
			// Quality gate: retry once if response is flat/boring
			if verdict := CheckResponseQuality(content); !verdict.Pass {
				if hint := QualityCorrectionHint(verdict.Failures); hint != "" {
					retryMsgs := make([]ai.Message, len(req.Messages), len(req.Messages)+2)
					copy(retryMsgs, req.Messages)
					retryMsgs = append(retryMsgs, ai.Message{Role: "assistant", Content: content}, ai.Message{Role: "system", Content: hint})
					retryReq := &ai.ChatRequest{Messages: retryMsgs, SystemPrompt: SystemPromptV2, MaxTokens: 2048, Temperature: ai.Float64(0.7), ModelHint: "fast"}
					if retryResp, err := o.aiProvider.ChatCompletion(ctx, retryReq); err == nil && retryResp.Content != "" {
						content = o.applySafetyFilter(retryResp.Content)
						cumulativeTokens += retryResp.TokensUsed
					}
				}
			}
			emitWithBubbleBreaks(content, emit)
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
	ch := make(chan ai.StreamChunk, 32)
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
	bb := &bubbleBreakBuffer{emit: emit}
	for chunk := range ch {
		if chunk.Content != "" {
			streamedContent.WriteString(chunk.Content)
			bb.Write(chunk.Content)
		}
		if chunk.Done {
			streamTokens = chunk.TokensUsed
		}
	}
	bb.Flush()

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

// buildActionChipsFromToolResults generates action chips based on tool result data.
func buildActionChipsFromToolResults(results []ToolResult) []ActionChip {
	var chips []ActionChip
	for _, r := range results {
		switch r.Name {
		case ToolGetAccountSummary:
			if spend, ok := r.Result["spend_balance"].(string); ok {
				if stash, ok := r.Result["stash_balance"].(string); ok {
					spendDec, _ := decimal.NewFromString(spend)
					stashDec, _ := decimal.NewFromString(stash)
					if spendDec.GreaterThan(decimal.NewFromInt(200)) && stashDec.LessThan(decimal.NewFromInt(10000)) {
						chips = append(chips, ActionChip{
							ID:    "transfer_to_stash",
							Label: "Transfer to Stash",
							Type:  "transfer_to_stash",
						})
					}
				}
			}
		case ToolGetBudget:
			if remaining, ok := r.Result["budget_remaining"].(string); ok {
				if limit, ok := r.Result["budget_limit"].(string); ok {
					remDec, _ := decimal.NewFromString(remaining)
					limDec, _ := decimal.NewFromString(limit)
					if limDec.IsPositive() && remDec.LessThan(limDec.Mul(decimal.NewFromFloat(0.2))) {
						chips = append(chips, ActionChip{
							ID:    "review_spending",
							Label: "Review spending",
							Type:  "review_spending",
						})
					}
				}
			}
		}
	}
	if len(chips) > 0 {
		chips = append(chips, ActionChip{
			ID:    "set_up_auto_transfer",
			Label: "Set up auto-transfer",
			Type:  "set_up_auto_transfer",
		})
	}
	if len(chips) > 3 {
		chips = chips[:3]
	}
	return chips
}

// thinkingMessage returns a user-facing thinking indicator for a tool execution.
func thinkingMessage(toolName string) string {
	switch toolName {
	case ToolGetSpendingSummary, ToolGetSpendingChart:
		return "Checking your spending..."
	case ToolGetRecentTransactions, ToolGetCardTransactions:
		return "Pulling your transactions..."
	case ToolGetMoneyFlow:
		return "Running the numbers..."
	case ToolGetAccountSummary:
		return "Checking your balances..."
	case ToolGetFinancialAudit:
		return "Running your financial audit..."
	case ToolGetFinancialHealth:
		return "Calculating your financial health..."
	case ToolGetDepositHistory:
		return "Looking at your deposits..."
	case ToolGetWithdrawalHistory:
		return "Checking your withdrawals..."
	case ToolGetYieldEarned:
		return "Checking your yield..."
	case ToolTransferFunds:
		return "Preparing your transfer..."
	case ToolGetBalanceHistory:
		return "Looking at your balance history..."
	case ToolGetRecurringExpenses:
		return "Scanning for recurring expenses..."
	case ToolSendMeme:
		return "Cooking up a meme..."
	case ToolSendVoiceMessage:
		return "Recording a voice message..."
	case ToolCelebrate:
		return "..."
	case ToolSendPoll:
		return "..."
	default:
		return "Working on it..."
	}
}

// looksFinancial returns true if the message likely involves financial data
// that would benefit from Supermemory context. Avoids wasted API calls for greetings.
func looksFinancial(msg string) bool {
	lower := strings.ToLower(msg)
	keywords := []string{
		"spend", "spent", "income", "salary", "earn", "money", "transfer",
		"airtime", "bill", "budget", "category", "month", "week",
		"how much", "total", "balance", "transaction", "payment",
		"bank", "statement", "receipt", "expensive", "cost",
		"who did i", "where did", "what did i", "compare",
		"recurring", "subscription", "utilities", "food", "transport",
		"finance", "financial", "overview", "summary",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
