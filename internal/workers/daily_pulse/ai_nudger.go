package daily_pulse

import (
	"context"
	"strings"

	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// AINudger generates personalized nudge text using the fast AI model.
type AINudger struct {
	provider ai.AIProvider
	logger   *zap.Logger
}

// NewAINudger creates a nudge generator backed by an AI provider.
func NewAINudger(provider ai.AIProvider, logger *zap.Logger) *AINudger {
	return &AINudger{provider: provider, logger: logger}
}

// GenerateNudge produces a single-sentence personalized nudge from a financial snapshot.
// Returns "" if AI is unavailable — caller should fall back to templates.
func (n *AINudger) GenerateNudge(ctx context.Context, snapshot string) string {
	resp, err := n.provider.ChatCompletion(ctx, &ai.ChatRequest{
		Messages: []ai.Message{{
			Role:    "user",
			Content: "Financial snapshot:\n" + snapshot,
		}},
		SystemPrompt: `You are Miriam, Rail's sharp but kind money coach. Write ONE habit-forming push notification body inspired by the best consumer app notifications, without copying any brand's wording. Rules:
- 8 to 18 words.
- Use the user's numbers when useful.
- Sound like a clever friend with taste, not a corporate alert.
- Create curiosity or a light reason to open Miriam chat.
- Never shame, guilt, threaten, overpromise returns, or give investment advice.
- No emojis, greetings, hashtags, or quotation marks.
Return only the notification body.`,
		MaxTokens:   60,
		Temperature: ai.Float64(0.9),
		ModelHint:   "fast",
	})
	if err != nil {
		n.logger.Debug("ai nudge generation failed", zap.Error(err))
		return ""
	}

	nudge := strings.TrimSpace(resp.Content)
	nudge = strings.Trim(nudge, "\"")
	if len(nudge) > 150 {
		nudge = nudge[:150]
	}
	return nudge
}
