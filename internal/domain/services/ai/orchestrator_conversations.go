package ai

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SetConversations sets the conversation persistence layer (optional).
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetConversations(c ConversationPersister) {
	o.conversations = c
}

// SetMemory sets the long-term memory service (optional).
func (o *AgentAdapter) SetMemory(m *MemoryService) {
	o.memory = m
}

// SetSupermemory sets the Supermemory client (optional).
func (o *AgentAdapter) SetSupermemory(s SupermemoryClient) {
	o.supermemory = s
}

// SetUsageTracker sets the usage tracking layer (optional).
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetUsageTracker(u UsageTracker) {
	o.usage = u
}

// ChatWithConversation handles a chat message using a persisted conversation
// for context. It loads summary + recent messages, calls the LLM, and persists
// the exchange. Tracks usage for cost monitoring.
func (o *AgentAdapter) ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatResponse, error) {
	return o.ChatWithConversationWithOptions(ctx, userID, conv, message, ChatOptions{})
}

func (o *AgentAdapter) ChatWithConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions) (*ChatResponse, error) {
	if o.conversations == nil {
		return o.ChatInContextWithOptions(ctx, userID, uuid.Nil, message, nil, opts)
	}

	history, err := o.conversations.BuildContext(ctx, conv)
	if err != nil {
		o.logger.Warn("failed to build conversation context, proceeding without history", zap.Error(err))
		history = nil
	}

	resp, err := o.ChatInContextWithOptions(ctx, userID, conv.ID, message, history, opts)
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

		// Auto-generate title from first message if conversation has no title
		if conv.Title == "" || conv.Title == "New conversation" {
			title := generateTitleFromMessage(message)
			if title != "" {
				if titleErr := o.conversations.UpdateTitle(persistCtx, conv.ID, title); titleErr != nil {
					o.logger.Warn("failed to auto-generate conversation title", zap.Error(titleErr))
				}
			}
		}

		if o.usage != nil {
			if trackErr := o.usage.TrackInteraction(persistCtx, userID, resp.Provider, resp.TokensUsed); trackErr != nil {
				o.logger.Error("failed to track usage", zap.Error(trackErr))
			}
		}

		// Extract facts and calibrate tone from this exchange
		if o.memory != nil {
			o.memory.ExtractMoment(userID, message, resp.Content)
		}

		// Ingest into Supermemory in its own goroutine — don't block the persist goroutine
		if o.supermemory != nil {
			go func(uid uuid.UUID, userMsg, assistantMsg string) {
				smCtx, smCancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer smCancel()
				_ = o.supermemory.IngestConversation(smCtx, uid.String(), []SupermemoryMessage{
					{Role: "user", Content: userMsg},
					{Role: "assistant", Content: assistantMsg},
				})
			}(userID, message, resp.Content)
		}
	}()

	return resp, nil
}

const voiceCostCeilingKeyPrefix = "voice_cost_ceiling:"
const voiceCostCeilingTTL = 60 * time.Second

// IsUserOverCostCeiling checks if a user has exceeded their monthly cost ceiling.
// CostCeilingResponse is returned when a user is over the cost ceiling.
// The handler can use this to inform the client about degraded mode.
type CostCeilingResponse struct {
	OverCeiling bool            `json:"over_ceiling"`
	CurrentCost decimal.Decimal `json:"current_cost_usd"`
	Ceiling     decimal.Decimal `json:"ceiling_usd"`
}

// generateTitleFromMessage creates a short title from the user's message,
// truncated to 50 chars at a word boundary.
func generateTitleFromMessage(msg string) string {
	title := strings.TrimSpace(msg)
	if title == "" {
		return ""
	}
	if len(title) <= 50 {
		return title
	}
	// Truncate at word boundary
	truncated := title[:50]
	if idx := strings.LastIndexByte(truncated, ' '); idx > 20 {
		truncated = truncated[:idx]
	}
	return truncated + "..."
}

// TrackVisionUsage records token usage from a vision API call.
func (o *AgentAdapter) TrackVisionUsage(ctx context.Context, userID uuid.UUID, tokens int) {
	if o.usage == nil || tokens <= 0 {
		return
	}
	if err := o.usage.TrackInteraction(ctx, userID, "gpt-4o-vision", tokens); err != nil {
		o.logger.Error("failed to track vision usage", zap.Error(err))
	}
}

// IngestVoiceTranscripts sends voice session transcripts to Supermemory.
// Runs async — caller does not wait.
// This is the ChatEngine-compatible version with a context parameter.
func (o *AgentAdapter) IngestVoiceTranscripts(ctx context.Context, userID uuid.UUID, pairs [][2]string) error {
	o.IngestVoiceTranscriptsOld(userID, pairs)
	return nil
}

// IngestVoiceTranscriptsOld sends voice session transcripts to Supermemory (legacy, no context).
func (o *AgentAdapter) IngestVoiceTranscriptsOld(userID uuid.UUID, pairs [][2]string) {
	if o.supermemory == nil || len(pairs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msgs := make([]SupermemoryMessage, 0, len(pairs)*2)
		for _, p := range pairs {
			if p[0] != "" {
				msgs = append(msgs, SupermemoryMessage{Role: "user", Content: p[0]})
			}
			if p[1] != "" {
				msgs = append(msgs, SupermemoryMessage{Role: "assistant", Content: p[1]})
			}
		}
		if len(msgs) > 0 {
			_ = o.supermemory.IngestConversation(ctx, userID.String(), msgs)
		}
	}()
}
