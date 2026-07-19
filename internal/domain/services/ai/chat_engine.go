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
	Chat(ctx context.Context, userID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error)
	ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error)
	ChatInContextWithOptions(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message, opts ChatOptions) (*ChatResponse, error)
	ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatResponse, error)
	ChatWithConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions) (*ChatResponse, error)

	// Stream sends a message and streams the response via SSE.
	ChatStream(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, emit func(StreamEvent)) error
	ChatStreamWithOptions(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, opts ChatOptions, emit func(StreamEvent)) error
	ChatStreamInConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions, emit func(StreamEvent)) error

	// Proactive content
	GetProactiveOpener(ctx context.Context, userID uuid.UUID) *ProactiveOpener
	GetConversationStarters(ctx context.Context, userID uuid.UUID) []ConversationStarter
	GenerateEnhancedNudge(ctx context.Context, userID uuid.UUID, req entities.EnhancedNudgeRequest) (*entities.EnhancedNudgeResponse, error)
	GenerateNudge(ctx context.Context, userID uuid.UUID, req NudgeRequest) (*NudgeResponse, error)
	GetProactiveVoiceInsight(ctx context.Context, userID uuid.UUID) string

	// Wrapped cards
	GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error)

	// Suggestions
	GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string

	// Voice
	BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string
	BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string
	BuildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{}

	// Operating plan
	StageOperatingPlanAction(ctx context.Context, userID, convID uuid.UUID, actionType string, params map[string]interface{}) (map[string]interface{}, error)

	// Action confirmation
	PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool)
	ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error)
	CancelAction(ctx context.Context, userID, convID uuid.UUID) error

	// Voice actions
	PrepareVoiceAction(ctx context.Context, userID, convID uuid.UUID, action string, params map[string]interface{}) (*entities.PendingAction, error)
	IngestVoiceTranscripts(ctx context.Context, userID uuid.UUID, pairs [][2]string) error

	// Vision usage tracking
	TrackVisionUsage(ctx context.Context, userID uuid.UUID, tokens int)

	// Tools (for voice handler direct execution)
	ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error)
	GetTools() []ai.Tool

	// Cost management
	IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool
}

// Verify ChatEngine is satisfied at compile time.
var _ ChatEngine = (*AgentAdapter)(nil)
