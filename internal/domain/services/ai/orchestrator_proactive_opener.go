package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// proactiveCache is a short-lived per-user cache to avoid redundant AI calls
// when the client fires multiple concurrent requests on screen load.
var proactiveCache = struct {
	sync.RWMutex
	entries map[uuid.UUID]proactiveCacheEntry
}{entries: make(map[uuid.UUID]proactiveCacheEntry)}

type proactiveCacheEntry struct {
	opener    *ProactiveOpener
	expiresAt time.Time
}

const proactiveCacheTTL = 60 * time.Second
const proactiveCacheMaxSize = 1000

func init() {
	go func() {
		for range time.Tick(2 * time.Minute) {
			now := time.Now()
			proactiveCache.Lock()
			for k, v := range proactiveCache.entries {
				if now.After(v.expiresAt) {
					delete(proactiveCache.entries, k)
				}
			}
			proactiveCache.Unlock()
		}
	}()
}

// ProactiveOpener is returned by the proactive opener endpoint.
type ProactiveOpener struct {
	Greeting      string       `json:"greeting"`
	BubbleMessage string       `json:"bubble_message"`
	Subtitle      string       `json:"subtitle,omitempty"`
	Severity      string       `json:"severity"`
	Suggestions   []Suggestion `json:"suggestions"`
	ActionChips   []ActionChip `json:"action_chips,omitempty"`
}

// Suggestion is a prompt suggestion shown to the user.
type Suggestion struct {
	Text     string `json:"text"`
	Category string `json:"category,omitempty"`
}

// ActionChip is a one-tap action button shown below a chat response.
type ActionChip struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

func (o *AgentAdapter) generateProactiveContent(ctx context.Context, userID uuid.UUID) (string, string, []Suggestion, []ActionChip) {
	snapshot := o.buildStarterContext(ctx, userID)

	now := time.Now().UTC()
	timeContext := fmt.Sprintf("Day: %s, time: %s", now.Format("Monday"), now.Format("3pm"))

	if snapshot == "" {
		snapshot = timeContext
	} else {
		snapshot = fmt.Sprintf("%s\n%s", snapshot, timeContext)
	}

	prompt := fmt.Sprintf(`Based on this user's financial snapshot, generate these three fields. Every field is REQUIRED.

1. "bubble_message": ONE very short teaser (3-7 words only) that appears in a chat bubble over Miriam on the home screen. Must be a complete, intriguing phrase that makes someone want to tap. Examples: "You spent 40 percent more on dining" or "Your savings hit a milestone" or "Overspent on groceries again". Do NOT use greetings like "Hey" or "Hi".

2. "greeting": ONE short, punchy greeting (max 12 words) for inside the chat. Lead with the most interesting or urgent thing — a spending pattern, a balance insight, a saving opportunity. Never generic.

3. "suggestions": exactly 4 short follow-up prompts (max 8 words each) specific to their numbers.

Return ONLY valid JSON, no markdown, no extra text:
{"bubble_message":"...","greeting":"...","suggestions":[{"text":"...","category":"spending|saving|insight|action"}...]}

User snapshot:
%s`, snapshot)

	resp, err := o.aiProvider.ChatCompletion(ctx, &ai.ChatRequest{
		Messages:     []ai.Message{{Role: "user", Content: prompt}},
		SystemPrompt: "You generate punchy, personalized conversation openers for a money app. Be specific with numbers. No emojis. Never generic. Return only valid JSON.",
		MaxTokens:    400,
		Temperature:  ai.Float64(0.7),
		ModelHint:    "fast",
	})
	if err != nil {
		o.logger.Debug("proactive opener: AI failed", zap.Error(err))
		return "", "", nil, nil
	}

	type openerResponse struct {
		BubbleMessage string       `json:"bubble_message"`
		Greeting      string       `json:"greeting"`
		Suggestions   []Suggestion `json:"suggestions"`
	}

	var parsed openerResponse
	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		o.logger.Debug("proactive opener: parse failed", zap.Error(err), zap.String("raw", content))
		return "", "", nil, nil
	}

	if parsed.Greeting == "" {
		return "", "", nil, nil
	}

	if len(parsed.Suggestions) > 4 {
		parsed.Suggestions = parsed.Suggestions[:4]
	}

	chips := o.generateActionChipsFromContext(ctx, userID)

	return parsed.BubbleMessage, parsed.Greeting, parsed.Suggestions, chips
}

func (o *AgentAdapter) generateActionChipsFromContext(ctx context.Context, userID uuid.UUID) []ActionChip {
	var chips []ActionChip

	spend, stash, _ := o.currentBalances(ctx, userID)

	// If there's idle surplus in Spend, suggest a transfer
	if spend.GreaterThan(decimal.NewFromInt(200)) && stash.LessThan(decimal.NewFromInt(10000)) {
		chips = append(chips, ActionChip{
			ID:    "transfer_to_stash",
			Label: "Transfer to Stash",
			Type:  "transfer_to_stash",
		})
	}

	// If there are no automations set up, suggest creating one
	chips = append(chips, ActionChip{
		ID:    "set_up_auto_transfer",
		Label: "Set up auto-transfer",
		Type:  "set_up_auto_transfer",
	})

	return chips
}

func (o *AgentAdapter) fallbackProactiveOpener() *ProactiveOpener {
	return &ProactiveOpener{
		BubbleMessage: "Let's check your finances",
		Greeting:      "Hey, I'm Miriam. I can audit your spending, plan the month, or catch the leaks.",
		Severity:      "info",
		Suggestions:   o.fallbackSuggestions(),
	}
}

func (o *AgentAdapter) fallbackSuggestions() []Suggestion {
	return []Suggestion{
		{Text: "Audit my money in hard mode", Category: "insight"},
		{Text: "Build my monthly plan", Category: "action"},
		{Text: "Find my biggest spending leak", Category: "spending"},
		{Text: "How is my stash doing?", Category: "saving"},
	}
}
