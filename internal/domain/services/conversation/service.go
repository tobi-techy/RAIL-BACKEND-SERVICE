package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Repository defines the persistence operations the service needs.
type Repository interface {
	CreateConversation(ctx context.Context, conv *entities.AIConversation) error
	GetConversation(ctx context.Context, id uuid.UUID) (*entities.AIConversation, error)
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error)
	DeleteConversation(ctx context.Context, id uuid.UUID) error
	UpdateSummary(ctx context.Context, id uuid.UUID, summary string) error
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
		msgs = append(msgs, ai.Message{Role: m.Role, Content: m.Content})
	}
	return msgs, nil
}

// RecordExchange persists a user message and assistant response, then
// triggers summarization if the threshold is reached.
func (s *Service) RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string) error {
	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        userMsg,
		EstimatedCost:  decimal.Zero,
		Model:          model,
	}); err != nil {
		return fmt.Errorf("save user message: %w", err)
	}

	if err := s.repo.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantMsg,
		TokenCount:     tokens,
		EstimatedCost:  cost,
		Model:          model,
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

	return nil
}

// summarize compresses conversation history into a compact summary.
// Runs with a 30s timeout to prevent goroutine leaks.
func (s *Service) summarize(convID uuid.UUID) {
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

	resp, err := s.summarizer.ChatCompletion(ctx, &ai.ChatRequest{
		SystemPrompt: "Summarize this conversation in under 200 words. Preserve key facts, user preferences, financial context, and any advice given. Be concise.",
		Messages:     []ai.Message{{Role: "user", Content: sb.String()}},
		MaxTokens:    300,
		Temperature:  0.3,
	})
	if err != nil {
		s.logger.Error("summarization: LLM call failed", zap.Error(err))
		return
	}

	if err := s.repo.UpdateSummary(ctx, convID, resp.Content); err != nil {
		s.logger.Error("summarization: failed to save summary", zap.Error(err))
		return
	}

	s.logger.Info("conversation summarized",
		zap.String("conversation_id", convID.String()),
		zap.Int("tokens_used", resp.TokensUsed),
	)
}
