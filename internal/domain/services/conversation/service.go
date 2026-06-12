package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var (
	summarizationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_ai_summarization_total",
			Help: "Total conversation summarizations by status",
		},
		[]string{"status"},
	)
	titleGenerationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_ai_title_generation_total",
			Help: "Total title generations by status",
		},
		[]string{"status"},
	)
)

// Repository defines the persistence operations the service needs.
type Repository interface {
	CreateConversation(ctx context.Context, conv *entities.AIConversation) error
	GetConversation(ctx context.Context, id uuid.UUID) (*entities.AIConversation, error)
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error)
	DeleteConversation(ctx context.Context, id uuid.UUID) error
	UpdateSummary(ctx context.Context, id uuid.UUID, summary string) error
	UpdateTitle(ctx context.Context, id uuid.UUID, title string) error
	IncrementStats(ctx context.Context, id uuid.UUID, tokens int, cost decimal.Decimal) error
	CreateMessage(ctx context.Context, msg *entities.AIMessage) error
	GetMessages(ctx context.Context, conversationID uuid.UUID, limit, offset int) ([]*entities.AIMessage, error)
	GetRecentMessages(ctx context.Context, conversationID uuid.UUID, n int) ([]*entities.AIMessage, error)
	CountMessages(ctx context.Context, conversationID uuid.UUID) (int, error)
}

// Summarizer generates conversation summaries.
type Summarizer interface {
	ChatCompletion(ctx context.Context, req *ai.ChatRequest) (*ai.ChatResponse, error)
}

// Service manages AI conversations and handles summarization.
type Service struct {
	repo       Repository
	summarizer Summarizer
	logger     *zap.Logger
}

// NewService creates a new conversation service.
func NewService(repo Repository, summarizer Summarizer, logger *zap.Logger) *Service {
	return &Service{repo: repo, summarizer: summarizer, logger: logger}
}

// CreateConversation starts a new conversation for a user.
func (s *Service) CreateConversation(ctx context.Context, userID uuid.UUID, title string) (*entities.AIConversation, error) {
	if title == "" {
		title = "New conversation"
	}
	conv := &entities.AIConversation{
		UserID:             userID,
		Title:              title,
		TotalEstimatedCost: decimal.Zero,
	}
	if err := s.repo.CreateConversation(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// GetConversation retrieves a conversation, returning nil if not found.
func (s *Service) GetConversation(ctx context.Context, id uuid.UUID) (*entities.AIConversation, error) {
	return s.repo.GetConversation(ctx, id)
}

// ListConversations returns a user's conversations.
func (s *Service) ListConversations(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListByUserID(ctx, userID, limit, offset)
}

// DeleteConversation removes a conversation owned by userID.
func (s *Service) DeleteConversation(ctx context.Context, userID, convID uuid.UUID) error {
	conv, err := s.repo.GetConversation(ctx, convID)
	if err != nil {
		return fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil || conv.UserID != userID {
		return fmt.Errorf("conversation not found")
	}
	return s.repo.DeleteConversation(ctx, convID)
}

// GetMessages returns messages for a conversation.
func (s *Service) GetMessages(ctx context.Context, conversationID uuid.UUID, limit, offset int) ([]*entities.AIMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.GetMessages(ctx, conversationID, limit, offset)
}

// BuildContext returns the LLM message history for a conversation:
// summary (if exists) + last N recent messages. This keeps input tokens
// bounded regardless of conversation length.
func (s *Service) BuildContext(ctx context.Context, conv *entities.AIConversation) ([]ai.Message, error) {
	var msgs []ai.Message

	if conv.SummaryContext != "" {
		msgs = append(msgs, ai.Message{
			Role:    "system",
			Content: "Previous conversation summary: " + conv.SummaryContext,
		})
	}

	recent, err := s.repo.GetRecentMessages(ctx, conv.ID, entities.RecentMessageWindow)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}

	for _, m := range recent {
		msg := ai.Message{Role: m.Role, Content: m.Content}
		// Reconstruct tool call metadata if persisted in message metadata.
		// This preserves multi-turn tool calling for conversations that
		// store tool_call_id and tool_calls in metadata.
		if m.Metadata != nil {
			if tcID, ok := m.Metadata["tool_call_id"].(string); ok && tcID != "" {
				msg.ToolCallID = tcID
			}
			if name, ok := m.Metadata["name"].(string); ok && name != "" {
				msg.Name = name
			}
			if tcs, ok := m.Metadata["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
				msg.ToolCalls = make([]ai.ToolCall, 0, len(tcs))
				for _, tc := range tcs {
					if tcMap, ok := tc.(map[string]interface{}); ok {
						toolCall := ai.ToolCall{}
						if id, ok := tcMap["id"].(string); ok {
							toolCall.ID = id
						}
						if name, ok := tcMap["name"].(string); ok {
							toolCall.Name = name
						}
						if args, ok := tcMap["arguments"].(map[string]interface{}); ok {
							toolCall.Arguments = args
						}
						msg.ToolCalls = append(msg.ToolCalls, toolCall)
					}
				}
			}
		}
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) == "" {
			if len(msg.ToolCalls) == 0 {
				s.logger.Warn("skipping empty assistant message in conversation context",
					zap.String("conversation_id", conv.ID.String()),
				)
				continue
			}
			msg.Content = "Calling tools..."
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// RecordExchange persists a user message and assistant response, then
// triggers summarization if the threshold is reached.
func (s *Service) RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        userMsg,
		EstimatedCost:  decimal.Zero,
		Model:          model,
	}); err != nil {
		return fmt.Errorf("save user message: %w", err)
	}

	// Store cards in assistant message metadata so they persist across sessions
	var metadata map[string]interface{}
	if len(cards) > 0 {
		metadata = map[string]interface{}{"cards": cards}
	}

	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantMsg,
		TokenCount:     tokens,
		EstimatedCost:  cost,
		Model:          model,
		Metadata:       metadata,
	}); err != nil {
		return fmt.Errorf("save assistant message: %w", err)
	}

	if err := s.repo.IncrementStats(ctx, convID, tokens, cost); err != nil {
		s.logger.Warn("failed to increment stats", zap.Error(err))
	}

	count, err := s.repo.CountMessages(ctx, convID)
	if err != nil {
		s.logger.Warn("failed to count messages", zap.Error(err))
		return nil
	}

	if count >= entities.SummarizationThreshold && count%entities.SummarizationThreshold == 0 {
		go s.summarize(convID)
	}

	// Auto-generate title after first exchange (count == 2 means first user+assistant pair)
	if count == 2 {
		go s.generateTitle(convID, userMsg)
	}

	return nil
}

// generateTitle creates a short title from the first user message.
func (s *Service) generateTitle(convID uuid.UUID, firstMessage string) {
	if s.summarizer == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := s.summarizer.ChatCompletion(ctx, &ai.ChatRequest{
		SystemPrompt: "Generate a short conversation title (max 6 words) for this user message. Return ONLY the title, no quotes or punctuation.",
		Messages:     []ai.Message{{Role: "user", Content: firstMessage}},
		MaxTokens:    20,
		Temperature:  ai.Float64(0.3),
	})
	if err != nil {
		titleGenerationTotal.WithLabelValues("error").Inc()
		s.logger.Warn("title generation failed", zap.Error(err))
		return
	}

	title := strings.TrimSpace(resp.Content)
	if title == "" || len(title) > 100 {
		return
	}

	if err := s.repo.UpdateTitle(ctx, convID, title); err != nil {
		titleGenerationTotal.WithLabelValues("error").Inc()
		s.logger.Warn("failed to save generated title", zap.Error(err))
		return
	}
	titleGenerationTotal.WithLabelValues("success").Inc()
}

// summarize compresses conversation history into a compact summary.
// Runs with a 30s timeout to prevent goroutine leaks.
//
// PII RISK: The conversation messages sent to the LLM for summarization may
// contain personal information (names, account numbers, addresses). The system
// prompt instructs the model to exclude PII from the summary output.
func (s *Service) summarize(convID uuid.UUID) {
	if s.summarizer == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Only fetch the last N messages to avoid exceeding LLM context window
	msgs, err := s.repo.GetRecentMessages(ctx, convID, entities.MaxSummarizationMessages)
	if err != nil {
		s.logger.Error("summarization: failed to get messages", zap.Error(err))
		return
	}

	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}

	sumReq := &ai.ChatRequest{
		SystemPrompt: "Do not include any personal information, account numbers, or addresses in the summary. Summarize this conversation in under 200 words. Preserve: key facts, user preferences, financial context, advice given, any receipt details (merchant, amount, date, category), image analysis results, and pending action items. Be concise.",
		Messages:     []ai.Message{{Role: "user", Content: sb.String()}},
		MaxTokens:    300,
		Temperature:  ai.Float64(0.3),
	}

	// Retry up to 2 times on failure
	var resp *ai.ChatResponse
	for attempt := 0; attempt < 2; attempt++ {
		resp, err = s.summarizer.ChatCompletion(ctx, sumReq)
		if err == nil {
			break
		}
		s.logger.Warn("summarization attempt failed", zap.Int("attempt", attempt+1), zap.Error(err))
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	if err != nil {
		summarizationTotal.WithLabelValues("error").Inc()
		s.logger.Error("summarization: all attempts failed", zap.Error(err))
		return
	}

	if err := s.repo.UpdateSummary(ctx, convID, resp.Content); err != nil {
		summarizationTotal.WithLabelValues("error").Inc()
		s.logger.Error("summarization: failed to save summary", zap.Error(err))
		return
	}

	summarizationTotal.WithLabelValues("success").Inc()
	s.logger.Info("conversation summarized",
		zap.String("conversation_id", convID.String()),
		zap.Int("tokens_used", resp.TokensUsed),
	)
}

// UpdateTitle updates the title of a conversation.
func (s *Service) UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error {
	return s.repo.UpdateTitle(ctx, convID, title)
}

// RecordImageExchange persists an image-based exchange with the thumbnail stored in metadata.
func (s *Service) RecordImageExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, thumbnail string, tokens int, model string) error {
	userMeta := map[string]interface{}{}
	if thumbnail != "" {
		userMeta["image_url"] = thumbnail
	}

	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        userMsg,
		EstimatedCost:  decimal.Zero,
		Model:          model,
		Metadata:       userMeta,
	}); err != nil {
		return fmt.Errorf("save user image message: %w", err)
	}

	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantMsg,
		TokenCount:     tokens,
		EstimatedCost:  decimal.Zero,
		Model:          model,
	}); err != nil {
		s.logger.Error("partial image exchange persist: assistant message failed after user message saved",
			zap.String("conversation_id", convID.String()),
			zap.Error(err))
		return fmt.Errorf("save assistant message: %w", err)
	}

	if err := s.repo.IncrementStats(ctx, convID, tokens, decimal.Zero); err != nil {
		s.logger.Warn("failed to increment stats for image exchange", zap.Error(err))
	}

	// Trigger summarization and title generation (same as RecordExchange)
	count, err := s.repo.CountMessages(ctx, convID)
	if err != nil {
		s.logger.Warn("failed to count messages for image exchange", zap.Error(err))
		return nil
	}
	if count >= entities.SummarizationThreshold && count%entities.SummarizationThreshold == 0 {
		go s.summarize(convID)
	}
	if count == 2 {
		go s.generateTitle(convID, userMsg)
	}

	return nil
}
