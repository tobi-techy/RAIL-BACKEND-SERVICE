package ai

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SetConversations sets the conversation persistence layer (optional).
func (o *Orchestrator) SetConversations(c ConversationPersister) {
	o.conversations = c
}

// ChatWithConversation handles a chat message using a persisted conversation
// for context. It loads summary + recent messages, calls the LLM, and persists
// the exchange.
func (o *Orchestrator) ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatResponse, error) {
	if o.conversations == nil {
		return o.Chat(ctx, userID, message, nil)
	}

	history, err := o.conversations.BuildContext(ctx, conv)
	if err != nil {
		o.logger.Warn("failed to build conversation context, proceeding without history", zap.Error(err))
		history = nil
	}

	resp, err := o.Chat(ctx, userID, message, history)
	if err != nil {
		return nil, err
	}

	// Rough cost estimate — will be replaced by proper model-aware pricing in Task 2
	cost := decimal.NewFromFloat(float64(resp.TokensUsed) * 0.00001)

	go func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if persistErr := o.conversations.RecordExchange(
			persistCtx, conv.ID,
			message, resp.Content,
			resp.TokensUsed, cost, resp.Provider,
		); persistErr != nil {
			o.logger.Error("failed to persist chat exchange",
				zap.Error(persistErr),
				zap.String("conversation_id", conv.ID.String()),
			)
		}
	}()

	return resp, nil
}
