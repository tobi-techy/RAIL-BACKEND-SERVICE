package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ChatEngineResponse is the ChatEngine-compatible response type.
type ChatEngineResponse struct {
	Content       string                  `json:"content"`
	Cards         []entities.InsightCard  `json:"cards,omitempty"`
	TokensUsed    int                     `json:"tokens_used"`
	Provider      string                  `json:"provider"`
	PendingAction *entities.PendingAction `json:"pending_action,omitempty"`
	PollQuestion  string                  `json:"poll_question,omitempty"`
	PollOptions   []string                `json:"poll_options,omitempty"`
	OpenURL       string                  `json:"open_url,omitempty"`
	OpenTitle     string                  `json:"open_title,omitempty"`
	Effect        string                  `json:"effect,omitempty"`
}

// ProactiveOpener is a proactive message opener for ChatEngine consumers.
type ProactiveOpener struct {
	Greeting      string                   `json:"greeting"`
	BubbleMessage string                   `json:"bubble_message"`
	Subtitle      string                   `json:"subtitle,omitempty"`
	Severity      string                   `json:"severity"`
	Suggestions   []Suggestion             `json:"suggestions"`
	ActionChips   []ConversationActionChip `json:"action_chips,omitempty"`
}

// Suggestion is a prompt suggestion shown to the user.
type Suggestion struct {
	Text     string `json:"text"`
	Category string `json:"category,omitempty"`
}

// ConversationActionChip is a quick-action chip shown in the UI.
type ConversationActionChip struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ConversationStarter is a suggested conversation starter for ChatEngine.
type ConversationStarter struct {
	Text     string `json:"text"`
	Category string `json:"category"`
}

// NudgeRequest describes the screen context for an ambient nudge.
type NudgeRequest struct {
	Screen   string `json:"screen"`
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
}

// NudgeResponse is the lightweight nudge returned to the client.
type NudgeResponse struct {
	Show     bool   `json:"show"`
	Message  string `json:"message,omitempty"`
	Severity string `json:"severity"`
	Shake    bool   `json:"shake"`
}

// chatViaCore calls the existing core Chat method and wraps the result.
func (a *Agent) chatViaCore(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message, opts ChatOptions) (*ChatEngineResponse, error) {
	coreResp, err := a.Chat(ctx, userID, convID, message, opts)
	if err != nil {
		return nil, err
	}

	resp := &ChatEngineResponse{
		Content:       coreResp.Content,
		TokensUsed:    coreResp.TokensUsed,
		Provider:      coreResp.Provider,
		PendingAction: convertPendingAction(coreResp.PendingAction),
		PollQuestion:  coreResp.PollQuestion,
		PollOptions:   coreResp.PollOptions,
		OpenURL:       coreResp.OpenURL,
		OpenTitle:     coreResp.OpenTitle,
		Effect:        coreResp.Effect,
	}

	if len(coreResp.Cards) > 0 {
		cardBytes, err := json.Marshal(coreResp.Cards)
		if err == nil {
			var cards []entities.InsightCard
			if json.Unmarshal(cardBytes, &cards) == nil {
				resp.Cards = cards
			}
		}
	}

	return resp, nil
}

// --- ChatWithConversation ---

func (a *Agent) ChatWithConversation(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string) (*ChatEngineResponse, error) {
	return a.ChatWithConversationWithOptions(ctx, userID, conv, message, ChatOptions{})
}

func (a *Agent) ChatWithConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions) (*ChatEngineResponse, error) {
	if a.deps.ConversationsPersister == nil {
		return a.chatViaCore(ctx, userID, conv.ID, message, nil, opts)
	}

	history, err := a.deps.ConversationsPersister.BuildContext(ctx, conv)
	if err != nil && a.deps.Logger != nil {
		a.deps.Logger.Warn("failed to build conversation context, proceeding without history", zap.Error(err))
		history = nil
	}

	resp, err := a.chatViaCore(ctx, userID, conv.ID, message, history, opts)
	if err != nil {
		return nil, err
	}

	cost := estimateCost(resp.Provider, resp.TokensUsed)
	go func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if persistErr := a.deps.ConversationsPersister.RecordExchange(
			persistCtx, conv.ID,
			message, resp.Content,
			resp.TokensUsed, cost, resp.Provider, resp.Cards,
		); persistErr != nil && a.deps.Logger != nil {
			a.deps.Logger.Error("failed to persist chat exchange",
				zap.Error(persistErr),
				zap.String("conversation_id", conv.ID.String()),
			)
		}

		if conv.Title == "" || conv.Title == "New conversation" {
			title := generateTitleFromMessage(message)
			if title != "" {
				if titleErr := a.deps.ConversationsPersister.UpdateTitle(persistCtx, conv.ID, title); titleErr != nil && a.deps.Logger != nil {
					a.deps.Logger.Warn("failed to auto-generate conversation title", zap.Error(titleErr))
				}
			}
		}

		if a.deps.UsageTrackerFn != nil {
			if trackErr := a.deps.UsageTrackerFn.TrackInteraction(persistCtx, userID, resp.Provider, resp.TokensUsed); trackErr != nil && a.deps.Logger != nil {
				a.deps.Logger.Error("failed to track usage", zap.Error(trackErr))
			}
		}

		if a.deps.Supermemory != nil {
			go func(uid uuid.UUID, userMsg, assistantMsg string) {
				smCtx, smCancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer smCancel()
				_ = a.deps.Supermemory.IngestConversation(smCtx, uid.String(), []SupermemoryMessage{
					{Role: "user", Content: userMsg},
					{Role: "assistant", Content: assistantMsg},
				})
			}(userID, message, resp.Content)
		}
	}()

	return resp, nil
}

// --- Streaming ---

func (a *Agent) ChatStream(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, emit func(StreamEvent)) error {
	return a.ChatStreamWithOptions(ctx, userID, message, history, ChatOptions{}, emit)
}

func (a *Agent) ChatStreamWithOptions(ctx context.Context, userID uuid.UUID, message string, history []ai.Message, opts ChatOptions, emit func(StreamEvent)) error {
	if a.deps.ChatStreamFn != nil {
		return a.deps.ChatStreamFn(ctx, userID, uuid.Nil, message, history, opts, emit)
	}
	resp, err := a.chatViaCore(ctx, userID, uuid.Nil, message, history, opts)
	if err != nil {
		return err
	}
	emit(StreamEvent{Type: "token", Token: resp.Content})
	emit(StreamEvent{Type: "done"})
	return nil
}

func (a *Agent) ChatStreamInConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts ChatOptions, emit func(StreamEvent)) error {
	if a.deps.ChatStreamFn != nil {
		return a.deps.ChatStreamFn(ctx, userID, conv.ID, message, nil, opts, emit)
	}
	return a.ChatStreamWithOptions(ctx, userID, message, nil, opts, emit)
}

// --- Proactive content (ChatEngine return types) ---

func (a *Agent) GetProactiveOpenerForChatEngine(ctx context.Context, userID uuid.UUID) *ProactiveOpener {
	return &ProactiveOpener{
		Greeting: "Hey! What's on your mind?",
		Severity: "info",
	}
}

func (a *Agent) GetConversationStartersForChatEngine(ctx context.Context, userID uuid.UUID) []ConversationStarter {
	if a.deps.GetConversationStartersFn != nil {
		if starters := a.deps.GetConversationStartersFn(ctx, userID); len(starters) > 0 {
			return starters
		}
	}

	starters := []ConversationStarter{
		{Text: "Where did my money go this month?", Category: "spending"},
		{Text: "What's my financial health score?", Category: "insight"},
	}

	if a.deps.Portfolio != nil {
		stats, err := a.deps.Portfolio.GetWeeklyStats(ctx, userID)
		if err == nil && stats != nil {
			if stats.WeeklyReturnPct.IsNegative() {
				starters = append(starters, ConversationStarter{Text: "Why is my portfolio down this week?", Category: "insight"})
			} else {
				starters = append(starters, ConversationStarter{Text: "How is my portfolio doing?", Category: "insight"})
			}
		}
	}

	if a.deps.Activity != nil {
		streak, err := a.deps.Activity.GetStreak(ctx, userID)
		if err == nil && streak != nil && streak.CurrentStreak > 3 {
			starters = append(starters, ConversationStarter{Text: "How long is my investing streak?", Category: "saving"})
		}
	}

	starters = append(starters, ConversationStarter{Text: "Set up an automation to save every Friday", Category: "action"})

	if len(starters) > 6 {
		starters = starters[:6]
	}
	return starters
}

// --- Nudge generation ---

func (a *Agent) GenerateEnhancedNudge(ctx context.Context, userID uuid.UUID, req entities.EnhancedNudgeRequest) (*entities.EnhancedNudgeResponse, error) {
	if a.deps.GenerateEnhancedNudgeFn != nil {
		result, err := a.deps.GenerateEnhancedNudgeFn(ctx, userID, req.Screen, req.Amount, req.Currency, req.TimeOfDay, req.DayOfWeek, req.DaysUntilPayday, req.MerchantHint)
		if err != nil {
			return nil, err
		}
		resp := &entities.EnhancedNudgeResponse{}
		if msg, ok := result["message"].(string); ok {
			resp.Message = msg
		}
		if sev, ok := result["severity"].(string); ok {
			resp.Severity = sev
		}
		if show, ok := result["show"].(bool); ok {
			resp.Show = show
		}
		return resp, nil
	}
	return &entities.EnhancedNudgeResponse{Show: false}, nil
}

func (a *Agent) GenerateNudge(ctx context.Context, userID uuid.UUID, req NudgeRequest) (*NudgeResponse, error) {
	if a.deps.GenerateNudgeFn != nil {
		result, err := a.deps.GenerateNudgeFn(ctx, userID, req.Screen, req.Amount, req.Currency)
		if err != nil {
			return nil, err
		}
		resp := &NudgeResponse{}
		if msg, ok := result["message"].(string); ok {
			resp.Message = msg
		}
		if sev, ok := result["severity"].(string); ok {
			resp.Severity = sev
		}
		if show, ok := result["show"].(bool); ok {
			resp.Show = show
		}
		if shake, ok := result["shake"].(bool); ok {
			resp.Shake = shake
		}
		return resp, nil
	}
	return &NudgeResponse{Show: false, Severity: "info"}, nil
}

func (a *Agent) GetProactiveVoiceInsight(ctx context.Context, userID uuid.UUID) string {
	if a.deps.GetProactiveVoiceInsightFn != nil {
		return a.deps.GetProactiveVoiceInsightFn(ctx, userID)
	}
	return ""
}

// --- Wrapped cards ---

func (a *Agent) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
	if a.deps.GenerateWrappedCardsFn != nil {
		rawCards, err := a.deps.GenerateWrappedCardsFn(ctx, userID)
		if err != nil {
			return nil, err
		}
		cards := make([]entities.WrappedCard, 0, len(rawCards))
		for _, rc := range rawCards {
			card := entities.WrappedCard{}
			if t, ok := rc["type"].(string); ok {
				card.Type = t
			}
			if t, ok := rc["title"].(string); ok {
				card.Title = t
			}
			if c, ok := rc["content"].(string); ok {
				card.Content = c
			}
			if d, ok := rc["data"].(map[string]interface{}); ok {
				card.Data = d
			}
			cards = append(cards, card)
		}
		return cards, nil
	}

	cards := make([]entities.WrappedCard, 0)

	if a.deps.Portfolio != nil {
		stats, err := a.deps.Portfolio.GetWeeklyStats(ctx, userID)
		if err == nil {
			returnPct := stats.WeeklyReturnPct.Mul(decimal.NewFromInt(100))
			direction := "up"
			if returnPct.LessThan(decimal.Zero) {
				direction = "down"
			}
			cards = append(cards, entities.WrappedCard{
				Type:    "performance_headline",
				Title:   "This Week's Vibe",
				Content: fmt.Sprintf("You're %s%.2f%% this week (%s)", getSign(returnPct), returnPct.Abs().InexactFloat64(), direction),
				Data:    map[string]interface{}{"weekly_return": returnPct.String()},
			})
		}
	}

	if a.deps.Activity != nil {
		now := time.Now()
		contributions, err := a.deps.Activity.GetContributions(ctx, userID, "all", now.AddDate(0, 0, -7), now)
		if err == nil {
			cards = append(cards, entities.WrappedCard{
				Type:    "contributions",
				Title:   "Money Moves",
				Content: fmt.Sprintf("$%s in deposits this week", contributions.Deposits.StringFixed(0)),
				Data:    map[string]interface{}{"deposits": contributions.Deposits.String(), "total": contributions.Total.String()},
			})
		}

		streak, err := a.deps.Activity.GetStreak(ctx, userID)
		if err == nil && streak.CurrentStreak > 0 {
			cards = append(cards, entities.WrappedCard{
				Type:    "streak",
				Title:   "On Fire",
				Content: fmt.Sprintf("%d day investing streak!", streak.CurrentStreak),
				Data:    map[string]interface{}{"current_streak": streak.CurrentStreak, "longest_streak": streak.LongestStreak},
			})
		}
	}

	return cards, nil
}

// --- Suggestions ---

func (a *Agent) GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string {
	if a.deps.GetPersonalizedSuggestionsFn != nil {
		return a.deps.GetPersonalizedSuggestionsFn(ctx, userID)
	}

	suggestions := []string{"Where did my money go this month?"}

	if a.deps.Portfolio != nil {
		stats, err := a.deps.Portfolio.GetWeeklyStats(ctx, userID)
		if err == nil && stats != nil {
			if stats.WeeklyReturnPct.IsNegative() {
				suggestions = append(suggestions, "Why is my portfolio down this week?")
			} else {
				suggestions = append(suggestions, "How is my portfolio doing?")
			}
		}
	}

	if a.deps.Activity != nil {
		streak, err := a.deps.Activity.GetStreak(ctx, userID)
		if err == nil && streak != nil && streak.CurrentStreak > 3 {
			suggestions = append(suggestions, "How long is my investing streak?")
		} else {
			suggestions = append(suggestions, "How can I build a saving habit?")
		}
	}

	suggestions = append(suggestions,
		"Forecast my end-of-month balance",
		"Set up an automation to save every Friday",
		"Create a savings goal with friends",
	)

	if len(suggestions) > 8 {
		suggestions = suggestions[:8]
	}
	return suggestions
}

// --- Voice ---

func (a *Agent) BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string {
	if a.deps.BuildRealtimeGreetingFn != nil {
		return a.deps.BuildRealtimeGreetingFn(ctx, userID)
	}
	return "Hey! What's on your mind?"
}

func (a *Agent) BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string {
	if a.deps.BuildRealtimeInstructionsFn != nil {
		return a.deps.BuildRealtimeInstructionsFn(ctx, userID)
	}
	return ""
}

func (a *Agent) BuildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	if a.deps.BuildRealtimeDynamicVarsFn != nil {
		return a.deps.BuildRealtimeDynamicVarsFn(ctx, userID)
	}
	return map[string]interface{}{"user_id": userID.String()}
}

// --- Operating plan ---

func (a *Agent) StageOperatingPlanAction(ctx context.Context, userID, convID uuid.UUID, actionType string, params map[string]interface{}) (map[string]interface{}, error) {
	if a.deps.StageOperatingPlanActionFn != nil {
		return a.deps.StageOperatingPlanActionFn(ctx, userID, convID, actionType, params)
	}
	return map[string]interface{}{"error": "operating plan actions are not available"}, nil
}

// --- Action confirmation ---

func (a *Agent) PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool) {
	if a.deps.PendingActions == nil {
		return nil, false
	}
	action := a.deps.PendingActions.Get(ctx, convID)
	if action == nil || action.UserID != userID {
		return nil, false
	}
	return action, true
}

func (a *Agent) ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error) {
	if a.deps.ConfirmActionFn != nil {
		return a.deps.ConfirmActionFn(ctx, userID, convID)
	}
	if a.deps.PendingActions == nil {
		return nil, fmt.Errorf("pending action store is unavailable")
	}
	action := a.deps.PendingActions.Get(ctx, convID)
	if action == nil {
		return nil, fmt.Errorf("action_expired: That action timed out. Just ask me again and I'll set it up fresh.")
	}
	if action.UserID != userID {
		return nil, fmt.Errorf("action does not belong to user")
	}
	return action, nil
}

func (a *Agent) CancelAction(ctx context.Context, userID, convID uuid.UUID) error {
	if a.deps.CancelActionFn != nil {
		return a.deps.CancelActionFn(ctx, userID, convID)
	}
	if a.deps.PendingActions == nil {
		return nil
	}
	action := a.deps.PendingActions.Get(ctx, convID)
	if action == nil {
		return nil
	}
	if action.UserID != userID {
		return fmt.Errorf("action does not belong to user")
	}
	a.deps.PendingActions.Delete(ctx, convID)
	return nil
}

// --- Voice actions ---

func (a *Agent) PrepareVoiceAction(ctx context.Context, userID, convID uuid.UUID, action string, params map[string]interface{}) (*entities.PendingAction, error) {
	if a.deps.PrepareVoiceActionFn != nil {
		return a.deps.PrepareVoiceActionFn(ctx, userID, convID, action, params)
	}
	return nil, fmt.Errorf("voice actions are not available")
}

func (a *Agent) IngestVoiceTranscripts(ctx context.Context, userID uuid.UUID, pairs [][2]string) error {
	if a.deps.Supermemory == nil || len(pairs) == 0 {
		return nil
	}
	go func() {
		smCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
			_ = a.deps.Supermemory.IngestConversation(smCtx, userID.String(), msgs)
		}
	}()
	return nil
}

// --- Vision usage ---

func (a *Agent) TrackVisionUsage(ctx context.Context, userID uuid.UUID, tokens int) {
	if a.deps.UsageTrackerFn == nil || tokens <= 0 {
		return
	}
	if err := a.deps.UsageTrackerFn.TrackInteraction(ctx, userID, "gpt-4o-vision", tokens); err != nil && a.deps.Logger != nil {
		a.deps.Logger.Error("failed to track vision usage", zap.Error(err))
	}
}

// --- Tool execution ---

func (a *Agent) ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	return a.ExecuteTool(ctx, userID, tc.Name, tc.Arguments)
}

// --- Helpers ---

func convertPendingAction(pa *PendingAction) *entities.PendingAction {
	if pa == nil {
		return nil
	}
	return &entities.PendingAction{
		ID:          uuid.New().String(),
		Action:      pa.Type,
		Description: pa.Description,
		Params:      pa.Params,
		ExpiresAt:   pa.ExpiresAt,
		CreatedAt:   time.Now(),
	}
}

func estimateCost(provider string, tokens int) decimal.Decimal {
	if tokens <= 0 {
		return decimal.Zero
	}
	costPerToken := decimal.NewFromFloat(0.00003)
	return costPerToken.Mul(decimal.NewFromInt(int64(tokens)))
}

func generateTitleFromMessage(msg string) string {
	title := strings.TrimSpace(msg)
	if title == "" {
		return ""
	}
	if len(title) <= 50 {
		return title
	}
	truncated := title[:50]
	if idx := strings.LastIndexByte(truncated, ' '); idx > 20 {
		truncated = truncated[:idx]
	}
	return truncated + "..."
}

func getSign(d decimal.Decimal) string {
	if d.GreaterThanOrEqual(decimal.Zero) {
		return "+"
	}
	return ""
}
