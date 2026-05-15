package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type failingAIProvider struct{}

func (f failingAIProvider) ChatCompletion(ctx context.Context, req *infraai.ChatRequest) (*infraai.ChatResponse, error) {
	return nil, errors.New("provider failed")
}

func (f failingAIProvider) ChatCompletionWithTools(ctx context.Context, req *infraai.ChatRequest, tools []infraai.Tool) (*infraai.ChatResponse, error) {
	return nil, errors.New("provider failed")
}

func (f failingAIProvider) Name() string { return "failing" }

func (f failingAIProvider) IsAvailable(ctx context.Context) bool { return true }

type recordingConversationPersister struct {
	recorded bool
}

func (r *recordingConversationPersister) BuildContext(ctx context.Context, conv *entities.AIConversation) ([]infraai.Message, error) {
	return nil, nil
}

func (r *recordingConversationPersister) RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
	r.recorded = true
	return nil
}

func TestChatStreamInConversationDoesNotPersistFailedEmptyResponse(t *testing.T) {
	persister := &recordingConversationPersister{}
	orchestrator := NewOrchestratorWithDeps(
		failingAIProvider{},
		nil,
		nil,
		nil,
		zap.NewNop(),
		OrchestratorDeps{Conversations: persister},
	)

	err := orchestrator.ChatStreamInConversationWithOptions(
		context.Background(),
		uuid.New(),
		&entities.AIConversation{ID: uuid.New(), UserID: uuid.New()},
		"hello",
		ChatOptions{},
		func(StreamEvent) {},
	)

	require.Error(t, err)
	assert.False(t, persister.recorded)
}
