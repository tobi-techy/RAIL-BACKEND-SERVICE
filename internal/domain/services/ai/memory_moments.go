package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ConversationMoment represents a memorable interaction worth calling back to.
type ConversationMoment struct {
	Type    string `json:"type"`    // "roast", "celebration", "challenge", "breakthrough", "callback"
	Summary string `json:"summary"` // short description: "roasted about ₦80k food spending"
	Topic   string `json:"topic"`   // "food_spending", "stash_milestone", "savings_streak" etc.
}

const momentExtractionPrompt = `Analyze this exchange between Miriam (financial assistant) and a user. Extract any MEMORABLE MOMENT worth calling back to in future conversations.

A moment is worth saving if:
- Miriam roasted/called out a specific bad habit (type: "roast")
- User hit a milestone or showed progress (type: "celebration")  
- Miriam challenged the user to do something (type: "challenge")
- User had a breakthrough realization about their money (type: "breakthrough")
- Either referenced a past conversation naturally (type: "callback")

Return JSON only. If no moment exists, return {"moment": null}
If a moment exists: {"moment": {"type": "...", "summary": "...", "topic": "..."}}

Keep summary under 60 chars. Topic should be a snake_case category.`

// ExtractMoment checks if a conversation exchange contains a memorable moment.
// Runs async after each exchange alongside fact extraction.
func (m *MemoryService) ExtractMoment(userID uuid.UUID, userMessage, assistantResponse string) {
	m.bgWrites.Add(1)
	go func() {
		defer m.bgWrites.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		moment := m.detectMoment(ctx, userMessage, assistantResponse)
		if moment == nil {
			return
		}

		// Save as a special fact category that the context builder can use for callbacks
		factText := fmt.Sprintf("[%s] %s", moment.Type, moment.Summary)
		fact := &MomentFact{
			UserID:    userID,
			Type:      moment.Type,
			Summary:   moment.Summary,
			Topic:     moment.Topic,
			CreatedAt: time.Now().UTC(),
		}

		if err := m.saveMomentAsFact(ctx, userID, fact, factText); err != nil {
			m.logger.Debug("moment save failed", zap.Error(err))
		}
	}()
}

// MomentFact is a conversation moment stored for future callbacks.
type MomentFact struct {
	UserID    uuid.UUID
	Type      string
	Summary   string
	Topic     string
	CreatedAt time.Time
}

func (m *MemoryService) detectMoment(ctx context.Context, userMessage, assistantResponse string) *ConversationMoment {
	if len(assistantResponse) < 30 {
		return nil // too short to be memorable
	}

	// Truncate to save tokens
	if len(userMessage) > 500 {
		userMessage = userMessage[:500]
	}
	if len(assistantResponse) > 800 {
		assistantResponse = assistantResponse[:800]
	}

	prompt := fmt.Sprintf("User: %s\n\nMiriam: %s", userMessage, assistantResponse)
	resp, err := m.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: momentExtractionPrompt,
		Messages:     []infraai.Message{{Role: "user", Content: prompt}},
		MaxTokens:    150,
		Temperature:  infraai.Float64(0.1),
		ModelHint:    "fast",
	})
	if err != nil {
		return nil
	}

	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Moment *ConversationMoment `json:"moment"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil
	}
	return result.Moment
}

func (m *MemoryService) saveMomentAsFact(ctx context.Context, userID uuid.UUID, moment *MomentFact, factText string) error {
	// Supersede older moments of the same type to prevent unbounded accumulation.
	// We match by type prefix ("[goal]", "[preference]", etc.) rather than topic substring
	// because topic matching via Contains was too fragile — partial matches like "money"
	// would incorrectly supersede unrelated facts containing that word. Type-based matching
	// ensures only one moment per category (e.g., one active goal, one active preference).
	var supersedes *uuid.UUID
	existing, err := m.store.GetActiveFactsByCategory(ctx, userID, "conversation_moment")
	if err == nil {
		for _, f := range existing {
			if strings.HasPrefix(f.Fact, "["+moment.Type+"]") {
				id := f.ID
				supersedes = &id
				break
			}
		}
	}

	fact := &entities.MiriamUserFact{
		UserID:     userID,
		Category:   "conversation_moment",
		Fact:       factText,
		Source:     entities.FactSourceConversation,
		Confidence: decimal.NewFromFloat(1.0),
	}
	return m.store.SaveFact(ctx, fact, supersedes)
}

// GetRecentCallbacks returns the summaries of the most recent stored conversation
// moments so the personality context can inject them as natural callbacks.
func (m *MemoryService) GetRecentCallbacks(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	facts, err := m.store.GetActiveFactsByCategory(ctx, userID, "conversation_moment")
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, limit)
	for _, f := range facts {
		if len(result) >= limit {
			break
		}
		result = append(result, strings.TrimSpace(f.Fact))
	}
	return result, nil
}
