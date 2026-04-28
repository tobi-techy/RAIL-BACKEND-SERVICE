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
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetConversations(c ConversationPersister) {
	o.conversations = c
}

// SetMemory sets the long-term memory service (optional).
func (o *Orchestrator) SetMemory(m *MemoryService) {
	o.memory = m
}

// SetUsageTracker sets the usage tracking layer (optional).
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetUsageTracker(u UsageTracker) {
	o.usage = u
}

// ChatWithConversation handles a chat message using a persisted conversation
// for context. It loads summary + recent messages, calls the LLM, and persists
// the exchange. Tracks usage for cost monitoring.
func (o *Orchestrator) ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatResponse, error) {
	if o.conversations == nil {
		return o.ChatInContext(ctx, userID, uuid.Nil, message, nil)
	}

	history, err := o.conversations.BuildContext(ctx, conv)
	if err != nil {
		o.logger.Warn("failed to build conversation context, proceeding without history", zap.Error(err))
		history = nil
	}

	resp, err := o.ChatInContext(ctx, userID, conv.ID, message, history)
	if err != nil {
		return nil, err
	}

	cost := entities.EstimateCost(resp.Provider, resp.TokensUsed)

	go func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if persistErr := o.conversations.RecordExchange(
			persistCtx, conv.ID,
			message, resp.Content,
			resp.TokensUsed, cost, resp.Provider, resp.Cards,
		); persistErr != nil {
			o.logger.Error("failed to persist chat exchange",
				zap.Error(persistErr),
				zap.String("conversation_id", conv.ID.String()),
			)
		}

		if o.usage != nil {
			if trackErr := o.usage.TrackInteraction(persistCtx, userID, resp.Provider, resp.TokensUsed); trackErr != nil {
				o.logger.Error("failed to track usage", zap.Error(trackErr))
			}
		}

		// Extract facts and calibrate tone from this exchange
		if o.memory != nil {
			o.memory.ProcessExchange(userID, message, resp.Content)
		}
	}()

	return resp, nil
}

// IsUserOverCostCeiling checks if a user has exceeded their monthly cost ceiling.
// Returns false if no usage tracker is configured.
func (o *Orchestrator) IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool {
	if o.usage == nil {
		return false
	}
	over, err := o.usage.IsOverCostCeiling(ctx, userID)
	if err != nil {
		o.logger.Warn("failed to check cost ceiling", zap.Error(err))
		return false
	}
	if over {
		observeCostCeilingHit()
	}
	return over
}

// CostCeilingResponse is returned when a user is over the cost ceiling.
// The handler can use this to inform the client about degraded mode.
type CostCeilingResponse struct {
	OverCeiling bool            `json:"over_ceiling"`
	CurrentCost decimal.Decimal `json:"current_cost_usd"`
	Ceiling     decimal.Decimal `json:"ceiling_usd"`
}

// TrackVisionUsage records token usage from a vision API call.
func (o *Orchestrator) TrackVisionUsage(ctx context.Context, userID uuid.UUID, tokens int) {
	if o.usage == nil || tokens <= 0 {
		return
	}
	if err := o.usage.TrackInteraction(ctx, userID, "gpt-4o-vision", tokens); err != nil {
		o.logger.Error("failed to track vision usage", zap.Error(err))
	}
}
