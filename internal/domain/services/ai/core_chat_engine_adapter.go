package ai

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// CoreChatEngineAdapter wraps *core.Agent and implements ChatEngine.
// This is the bridge that lets us swap AgentAdapter for core.Agent
// without changing any handler or worker code.
type CoreChatEngineAdapter struct {
	agent *core.Agent
}

// NewCoreChatEngineAdapter creates a ChatEngine backed by core.Agent.
func NewCoreChatEngineAdapter(agent *core.Agent) *CoreChatEngineAdapter {
	return &CoreChatEngineAdapter{agent: agent}
}

// --- Chat methods ---

func (a *CoreChatEngineAdapter) Chat(ctx context.Context, userID uuid.UUID, message string, history []infraai.Message) (*ChatResponse, error) {
	resp, err := a.agent.ChatWithConversationWithOptions(ctx, userID, &entities.AIConversation{}, message, convertCoreChatOptions(ChatOptions{}))
	if err != nil {
		return nil, err
	}
	return convertChatEngineResponse(resp), nil
}

func (a *CoreChatEngineAdapter) ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []infraai.Message) (*ChatResponse, error) {
	return a.ChatInContextWithOptions(ctx, userID, convID, message, history, ChatOptions{})
}

func (a *CoreChatEngineAdapter) ChatInContextWithOptions(ctx context.Context, userID, convID uuid.UUID, message string, history []infraai.Message, opts ChatOptions) (*ChatResponse, error) {
	conv := &entities.AIConversation{ID: convID}
	resp, err := a.agent.ChatWithConversationWithOptions(ctx, userID, conv, message, convertCoreChatOptions(opts))
	if err != nil {
		return nil, err
	}
	return convertChatEngineResponse(resp), nil
}

func (a *CoreChatEngineAdapter) ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatResponse, error) {
	return a.ChatWithConversationWithOptions(ctx, userID, conv, message, ChatOptions{})
}

func (a *CoreChatEngineAdapter) ChatWithConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions) (*ChatResponse, error) {
	resp, err := a.agent.ChatWithConversationWithOptions(ctx, userID, conv, message, convertCoreChatOptions(opts))
	if err != nil {
		return nil, err
	}
	return convertChatEngineResponse(resp), nil
}

// --- Streaming ---

func (a *CoreChatEngineAdapter) ChatStream(ctx context.Context, userID uuid.UUID, message string, history []infraai.Message, emit func(StreamEvent)) error {
	return a.ChatStreamWithOptions(ctx, userID, message, history, ChatOptions{}, emit)
}

func (a *CoreChatEngineAdapter) ChatStreamWithOptions(ctx context.Context, userID uuid.UUID, message string, history []infraai.Message, opts ChatOptions, emit func(StreamEvent)) error {
	wrappedEmit := func(event core.StreamEvent) {
		emit(convertCoreStreamEvent(event))
	}
	return a.agent.ChatStreamWithOptions(ctx, userID, message, history, convertCoreChatOptions(opts), wrappedEmit)
}

func (a *CoreChatEngineAdapter) ChatStreamInConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions, emit func(StreamEvent)) error {
	wrappedEmit := func(event core.StreamEvent) {
		emit(convertCoreStreamEvent(event))
	}
	return a.agent.ChatStreamInConversationWithOptions(ctx, userID, conv, message, convertCoreChatOptions(opts), wrappedEmit)
}

// --- Proactive content ---

func (a *CoreChatEngineAdapter) GetProactiveOpener(ctx context.Context, userID uuid.UUID) *ProactiveOpener {
	coreOpener := a.agent.GetProactiveOpenerForChatEngine(ctx, userID)
	return &ProactiveOpener{
		Greeting:      coreOpener.Greeting,
		BubbleMessage: coreOpener.BubbleMessage,
		Subtitle:      coreOpener.Subtitle,
		Severity:      coreOpener.Severity,
		Suggestions:   convertCoreSuggestions(coreOpener.Suggestions),
		ActionChips:   convertCoreActionChips(coreOpener.ActionChips),
	}
}

func (a *CoreChatEngineAdapter) GetConversationStarters(ctx context.Context, userID uuid.UUID) []ConversationStarter {
	coreStarters := a.agent.GetConversationStartersForChatEngine(ctx, userID)
	starters := make([]ConversationStarter, len(coreStarters))
	for i, s := range coreStarters {
		starters[i] = ConversationStarter{Text: s.Text, Category: s.Category}
	}
	return starters
}

func (a *CoreChatEngineAdapter) GenerateEnhancedNudge(ctx context.Context, userID uuid.UUID, req entities.EnhancedNudgeRequest) (*entities.EnhancedNudgeResponse, error) {
	return a.agent.GenerateEnhancedNudge(ctx, userID, req)
}

func (a *CoreChatEngineAdapter) GenerateNudge(ctx context.Context, userID uuid.UUID, req NudgeRequest) (*NudgeResponse, error) {
	coreReq := core.NudgeRequest{Screen: req.Screen, Amount: req.Amount, Currency: req.Currency}
	resp, err := a.agent.GenerateNudge(ctx, userID, coreReq)
	if err != nil {
		return nil, err
	}
	return &NudgeResponse{Show: resp.Show, Message: resp.Message, Severity: resp.Severity, Shake: resp.Shake}, nil
}

func (a *CoreChatEngineAdapter) GetProactiveVoiceInsight(ctx context.Context, userID uuid.UUID) string {
	return a.agent.GetProactiveVoiceInsight(ctx, userID)
}

// --- Wrapped cards ---

func (a *CoreChatEngineAdapter) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
	return a.agent.GenerateWrappedCards(ctx, userID)
}

// --- Suggestions ---

func (a *CoreChatEngineAdapter) GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string {
	return a.agent.GetPersonalizedSuggestions(ctx, userID)
}

// --- Voice ---

func (a *CoreChatEngineAdapter) BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string {
	return a.agent.BuildRealtimeGreeting(ctx, userID)
}

func (a *CoreChatEngineAdapter) BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string {
	return a.agent.BuildRealtimeInstructions(ctx, userID)
}

func (a *CoreChatEngineAdapter) BuildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	return a.agent.BuildRealtimeDynamicVars(ctx, userID)
}

// --- Operating plan ---

func (a *CoreChatEngineAdapter) StageOperatingPlanAction(ctx context.Context, userID, convID uuid.UUID, actionType string, params map[string]interface{}) (map[string]interface{}, error) {
	return a.agent.StageOperatingPlanAction(ctx, userID, convID, actionType, params)
}

// --- Action confirmation ---

func (a *CoreChatEngineAdapter) PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool) {
	return a.agent.PeekPendingAction(ctx, userID, convID)
}

func (a *CoreChatEngineAdapter) ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error) {
	return a.agent.ConfirmAction(ctx, userID, convID)
}

func (a *CoreChatEngineAdapter) CancelAction(ctx context.Context, userID, convID uuid.UUID) error {
	return a.agent.CancelAction(ctx, userID, convID)
}

// --- Voice actions ---

func (a *CoreChatEngineAdapter) PrepareVoiceAction(ctx context.Context, userID, convID uuid.UUID, action string, params map[string]interface{}) (*entities.PendingAction, error) {
	return a.agent.PrepareVoiceAction(ctx, userID, convID, action, params)
}

func (a *CoreChatEngineAdapter) IngestVoiceTranscripts(ctx context.Context, userID uuid.UUID, pairs [][2]string) error {
	return a.agent.IngestVoiceTranscripts(ctx, userID, pairs)
}

// --- Vision usage ---

func (a *CoreChatEngineAdapter) TrackVisionUsage(ctx context.Context, userID uuid.UUID, tokens int) {
	a.agent.TrackVisionUsage(ctx, userID, tokens)
}

// --- Tool execution ---

func (a *CoreChatEngineAdapter) ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc infraai.ToolCall) (map[string]interface{}, error) {
	return a.agent.ExecuteToolPublic(ctx, userID, tc)
}

func (a *CoreChatEngineAdapter) GetTools() []infraai.Tool {
	return a.agent.GetAllTools()
}

// --- Cost management ---

func (a *CoreChatEngineAdapter) IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool {
	return a.agent.IsUserOverCostCeiling(ctx, userID)
}

// --- Type conversion helpers ---

func convertChatEngineResponse(resp *core.ChatEngineResponse) *ChatResponse {
	if resp == nil {
		return nil
	}
	return &ChatResponse{
		Content:       resp.Content,
		Cards:         resp.Cards,
		TokensUsed:    resp.TokensUsed,
		Provider:      resp.Provider,
		PendingAction: resp.PendingAction,
		PollQuestion:  resp.PollQuestion,
		PollOptions:   resp.PollOptions,
		OpenURL:       resp.OpenURL,
		OpenTitle:     resp.OpenTitle,
		Effect:        resp.Effect,
	}
}

func convertCoreChatOptions(opts ChatOptions) core.ChatOptions {
	return core.ChatOptions{
		ToneMode:  opts.ToneMode,
		ModelHint: opts.ModelHint,
	}
}

func convertCoreStreamEvent(event core.StreamEvent) StreamEvent {
	return StreamEvent{
		Type:    event.Type,
		Content: event.Token,
	}
}

func convertCoreSuggestions(suggestions []core.Suggestion) []Suggestion {
	if suggestions == nil {
		return nil
	}
	result := make([]Suggestion, len(suggestions))
	for i, s := range suggestions {
		result[i] = Suggestion{Text: s.Text, Category: s.Category}
	}
	return result
}

func convertCoreActionChips(chips []core.ConversationActionChip) []ActionChip {
	if chips == nil {
		return nil
	}
	result := make([]ActionChip, len(chips))
	for i, c := range chips {
		result[i] = ActionChip{ID: c.ID, Label: c.Label, Type: c.Type, Description: c.Description}
	}
	return result
}

// Ensure CoreChatEngineAdapter satisfies ChatEngine at compile time.
var _ ChatEngine = (*CoreChatEngineAdapter)(nil)
