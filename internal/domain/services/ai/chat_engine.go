package ai

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ChatEngine defines the interface for Miriam's conversational AI.
// Consumers (handlers, workers) should depend on this interface, not *Orchestrator directly.
// This enables testing, substitution, and future decomposition of the god object.
type ChatEngine interface {
	// Chat sends a message and returns a response (non-streaming).
	ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error)
	ChatInContextWithOptions(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message, opts ChatOptions) (*ChatResponse, error)

	// Stream sends a message and streams the response via SSE.
	ChatStream(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, emit func(StreamEvent)) error
	ChatStreamWithOptions(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, opts ChatOptions, emit func(StreamEvent)) error
	ChatStreamInConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions, emit func(StreamEvent)) error

	// Proactive content
	GetProactiveOpener(ctx context.Context, userID uuid.UUID) *ProactiveOpener
	GetConversationStarters(ctx context.Context, userID uuid.UUID) []ConversationStarter
	GenerateEnhancedNudge(ctx context.Context, userID uuid.UUID, req entities.EnhancedNudgeRequest) (*entities.EnhancedNudgeResponse, error)

	// Voice
	BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string
	BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string
	BuildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{}

	// Tools (for voice handler direct execution)
	ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error)
	GetTools() []ai.Tool

	// Cost management
	IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool
}

// Verify Orchestrator satisfies ChatEngine at compile time.
var _ ChatEngine = (*Orchestrator)(nil)
