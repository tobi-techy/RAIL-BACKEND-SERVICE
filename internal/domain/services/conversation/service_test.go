package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockRepo is an in-memory implementation of Repository for testing.
type mockRepo struct {
	mu            sync.RWMutex
	conversations map[uuid.UUID]*entities.AIConversation
	messages      map[uuid.UUID][]*entities.AIMessage
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		conversations: make(map[uuid.UUID]*entities.AIConversation),
		messages:      make(map[uuid.UUID][]*entities.AIMessage),
	}
}

func (r *mockRepo) CreateConversation(ctx context.Context, conv *entities.AIConversation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conv.ID == uuid.Nil {
		conv.ID = uuid.New()
	}
	r.conversations[conv.ID] = conv
	return nil
}

func (r *mockRepo) GetConversation(ctx context.Context, id uuid.UUID) (*entities.AIConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conv, ok := r.conversations[id]
	if !ok {
		return nil, nil
	}
	return conv, nil
}

func (r *mockRepo) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*entities.AIConversation
	for _, conv := range r.conversations {
		if conv.UserID == userID {
			result = append(result, conv)
		}
	}
	if offset >= len(result) {
		return nil, nil
	}
	end := offset + limit
	if end > len(result) {
		end = len(result)
	}
	return result[offset:end], nil
}

func (r *mockRepo) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conversations, id)
	return nil
}

func (r *mockRepo) UpdateSummary(ctx context.Context, id uuid.UUID, summary string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conv, ok := r.conversations[id]; ok {
		conv.SummaryContext = summary
	}
	return nil
}

func (r *mockRepo) UpdateTitle(ctx context.Context, id uuid.UUID, title string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conv, ok := r.conversations[id]; ok {
		conv.Title = title
	}
	return nil
}

func (r *mockRepo) IncrementStats(ctx context.Context, id uuid.UUID, tokens int, cost decimal.Decimal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conv, ok := r.conversations[id]; ok {
		conv.TotalTokens += tokens
		conv.TotalEstimatedCost = conv.TotalEstimatedCost.Add(cost)
	}
	return nil
}

func (r *mockRepo) CreateMessage(ctx context.Context, msg *entities.AIMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages[msg.ConversationID] = append(r.messages[msg.ConversationID], msg)
	return nil
}

func (r *mockRepo) GetMessages(ctx context.Context, conversationID uuid.UUID, limit, offset int) ([]*entities.AIMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msgs := r.messages[conversationID]
	return msgs, nil
}

func (r *mockRepo) GetRecentMessages(ctx context.Context, conversationID uuid.UUID, n int) ([]*entities.AIMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msgs := r.messages[conversationID]
	if len(msgs) <= n {
		return msgs, nil
	}
	return msgs[len(msgs)-n:], nil
}

func (r *mockRepo) CountMessages(ctx context.Context, conversationID uuid.UUID) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.messages[conversationID]), nil
}

// mockSummarizer is a test double for the Summarizer interface.
type mockSummarizer struct {
	resp *ai.ChatResponse
	err  error
}

func (m *mockSummarizer) ChatCompletion(ctx context.Context, req *ai.ChatRequest) (*ai.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

func TestServiceCreateConversation(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	userID := uuid.New()
	conv, err := svc.CreateConversation(context.Background(), userID, "Test Title")
	require.NoError(t, err)
	assert.Equal(t, userID, conv.UserID)
	assert.Equal(t, "Test Title", conv.Title)
	assert.NotEqual(t, uuid.Nil, conv.ID)
}

func TestServiceBuildContext(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{
		ID:             convID,
		UserID:         uuid.New(),
		SummaryContext: "User likes tech stocks",
	}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	// Add messages
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        "What's my balance?",
	}))
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        "Your balance is $500",
	}))

	ctx, err := svc.BuildContext(context.Background(), conv)
	require.NoError(t, err)
	require.Len(t, ctx, 3)

	assert.Equal(t, "system", ctx[0].Role)
	assert.Contains(t, ctx[0].Content, "User likes tech stocks")

	assert.Equal(t, "user", ctx[1].Role)
	assert.Equal(t, "What's my balance?", ctx[1].Content)

	assert.Equal(t, "assistant", ctx[2].Role)
	assert.Equal(t, "Your balance is $500", ctx[2].Content)
}

func TestServiceBuildContextWithToolCalls(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	// Add an assistant message with tool_calls in metadata
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        "",
		Metadata: map[string]interface{}{
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id":        "call_abc",
					"name":      "get_balance",
					"arguments": map[string]interface{}{"user_id": "123"},
				},
			},
		},
	}))

	// Add a tool message with tool_call_id in metadata
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "tool",
		Content:        `{"balance": 500}`,
		Metadata: map[string]interface{}{
			"tool_call_id": "call_abc",
			"name":         "get_balance",
		},
	}))

	msgs, err := svc.BuildContext(context.Background(), conv)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	// Assistant message should have ToolCalls reconstructed
	assert.Equal(t, "assistant", msgs[0].Role)
	assert.Equal(t, "Calling tools...", msgs[0].Content)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, "call_abc", msgs[0].ToolCalls[0].ID)
	assert.Equal(t, "get_balance", msgs[0].ToolCalls[0].Name)
	assert.Equal(t, "123", msgs[0].ToolCalls[0].Arguments["user_id"])

	// Tool message should have ToolCallID and Name reconstructed
	assert.Equal(t, "tool", msgs[1].Role)
	assert.Equal(t, "call_abc", msgs[1].ToolCallID)
	assert.Equal(t, "get_balance", msgs[1].Name)
}

func TestServiceBuildContextSkipsEmptyAssistantMessages(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        "hello",
	}))
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        "",
	}))
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        "what's my balance?",
	}))

	msgs, err := svc.BuildContext(context.Background(), conv)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "hello", msgs[0].Content)
	assert.Equal(t, "user", msgs[1].Role)
	assert.Equal(t, "what's my balance?", msgs[1].Content)
}

func TestServiceRecordExchange(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	err := svc.RecordExchange(context.Background(), convID,
		"What's my balance?", "Your balance is $500",
		15, decimal.NewFromFloat(0.00015), "gpt-4o", nil,
	)
	require.NoError(t, err)

	msgs, err := repo.GetMessages(context.Background(), convID, 100, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)

	// Stats should be updated
	updatedConv, err := repo.GetConversation(context.Background(), convID)
	require.NoError(t, err)
	assert.Equal(t, 15, updatedConv.TotalTokens)
}

func TestServiceRecordExchangeWithCards(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	cards := []entities.InsightCard{
		{Type: "balance", Title: "Balance", Subtitle: "$500"},
	}

	err := svc.RecordExchange(context.Background(), convID,
		"Show me my balance", "Here's your balance",
		10, decimal.NewFromFloat(0.0001), "gpt-4o", cards,
	)
	require.NoError(t, err)

	msgs, err := repo.GetMessages(context.Background(), convID, 100, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	// Assistant message should have cards in metadata
	assistantMsg := msgs[1]
	require.NotNil(t, assistantMsg.Metadata)
	assert.Contains(t, assistantMsg.Metadata, "cards")
}

func TestServiceDeleteConversation(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	userID := uuid.New()
	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: userID}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	err := svc.DeleteConversation(context.Background(), userID, convID)
	require.NoError(t, err)
	deletedConv, _ := repo.GetConversation(context.Background(), convID)
	assert.Nil(t, deletedConv)
}

func TestServiceDeleteConversationWrongUser(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	err := svc.DeleteConversation(context.Background(), uuid.New(), convID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestServiceListConversations(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	userID := uuid.New()
	for i := 0; i < 5; i++ {
		conv := &entities.AIConversation{ID: uuid.New(), UserID: userID}
		require.NoError(t, repo.CreateConversation(context.Background(), conv))
	}

	convs, err := svc.ListConversations(context.Background(), userID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, convs, 5)
}

func TestServiceListConversationsLimitBounds(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	userID := uuid.New()
	for i := 0; i < 100; i++ {
		conv := &entities.AIConversation{ID: uuid.New(), UserID: userID}
		require.NoError(t, repo.CreateConversation(context.Background(), conv))
	}

	// Limit > 50 is clamped to default (20)
	convs, err := svc.ListConversations(context.Background(), userID, 100, 0)
	require.NoError(t, err)
	assert.Len(t, convs, 20)
}

func TestServiceGetMessagesBounds(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	// Limit > 100 should be clamped to 100
	msgs, err := svc.GetMessages(context.Background(), convID, 200, 0)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestServiceGenerateTitle(t *testing.T) {
	repo := newMockRepo()
	summarizer := &mockSummarizer{resp: &ai.ChatResponse{Content: "Balance Inquiry"}}
	svc := NewService(repo, summarizer, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New()}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	// RecordExchange creates 2 messages (user + assistant).
	// Count becomes 2, triggering title generation.
	err := svc.RecordExchange(context.Background(), convID,
		"What's my balance?", "Your balance is $500",
		10, decimal.Zero, "gpt-4o", nil,
	)
	require.NoError(t, err)

	// Wait for goroutine
	time.Sleep(100 * time.Millisecond)

	updatedConv, err := repo.GetConversation(context.Background(), convID)
	require.NoError(t, err)
	assert.Equal(t, "Balance Inquiry", updatedConv.Title)
}

func TestServiceGenerateTitleEmptyResponse(t *testing.T) {
	repo := newMockRepo()
	summarizer := &mockSummarizer{resp: &ai.ChatResponse{Content: ""}}
	svc := NewService(repo, summarizer, zap.NewNop())

	convID := uuid.New()
	conv := &entities.AIConversation{ID: convID, UserID: uuid.New(), Title: "Original"}
	require.NoError(t, repo.CreateConversation(context.Background(), conv))

	// Simulate exchange
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{ConversationID: convID, Role: "user", Content: "hi"}))
	require.NoError(t, repo.CreateMessage(context.Background(), &entities.AIMessage{ConversationID: convID, Role: "assistant", Content: "hello"}))

	err := svc.RecordExchange(context.Background(), convID, "hi", "hello", 5, decimal.Zero, "gpt-4o", nil)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Title should remain unchanged because response was empty
	unchangedConv, _ := repo.GetConversation(context.Background(), convID)
	assert.Equal(t, "Original", unchangedConv.Title)
}
