package platform

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	aiprompt "github.com/rail-service/rail_service/internal/domain/services/ai/prompt"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

const proactiveComposerPrompt = `
You are rewriting a deterministic proactive financial notification that Miriam
will send without a user message. Preserve every fact, number, currency symbol,
merchant, and action exactly. Add no facts or numbers.

Write in Miriam's voice: lowercase by default, human, direct, warm, and
specific. Plain text only, one to three short sentences. No greeting, title,
"Miriam", emojis, bullets, markdown, or em/en dashes. Do not ask a question
unless the draft already requires a user response. Return only the rewritten
message.`

var proactiveNumberToken = regexp.MustCompile(`(?:[$₦£€]\s*)?\d[\d,]*(?:\.\d+)?%?`)

// ProactiveComposer gives all unprompted platform messages the same personality
// contract as chat. Implementations must return the original draft when they
// cannot prove the rewrite preserved its numeric facts.
type ProactiveComposer interface {
	Compose(ctx context.Context, draft string) string
}

type proactiveComposer struct {
	provider ai.AIProvider
	logger   *zap.Logger
}

// NewProactiveComposer creates a conservative best-effort composer. A nil
// provider is valid and simply leaves every deterministic draft unchanged.
func NewProactiveComposer(provider ai.AIProvider, logger *zap.Logger) ProactiveComposer {
	return &proactiveComposer{provider: provider, logger: logger}
}

func (c *proactiveComposer) Compose(ctx context.Context, draft string) string {
	draft = strings.TrimSpace(draft)
	if draft == "" || c == nil || c.provider == nil {
		return draft
	}

	requestCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	resp, err := c.provider.ChatCompletion(requestCtx, &ai.ChatRequest{
		Messages:     []ai.Message{{Role: "user", Content: "Draft:\n" + draft}},
		SystemPrompt: aiprompt.SystemPromptV2 + proactiveComposerPrompt,
		MaxTokens:    150,
		Temperature:  ai.Float64(0.4),
		ModelHint:    "fast",
	})
	if err != nil {
		c.logFallback("generation failed", err)
		return draft
	}

	out := strings.Trim(strings.TrimSpace(resp.Content), `"`)
	if !validProactiveRewrite(draft, out) {
		c.logFallback("rewrite rejected", nil)
		return draft
	}
	return out
}

func (c *proactiveComposer) logFallback(reason string, err error) {
	if c.logger != nil {
		c.logger.Debug("proactive message composer fallback", zap.String("reason", reason), zap.Error(err))
	}
}

func validProactiveRewrite(draft, out string) bool {
	if out == "" || len(out) > 280 || !sameNumberTokens(draft, out) {
		return false
	}
	if strings.ContainsAny(out, "•‣⚠️\u2013\u2014") ||
		strings.Contains(out, "**") ||
		strings.Contains(strings.ToLower(out), "miriam") {
		return false
	}
	return true
}

func sameNumberTokens(a, b string) bool {
	aTokens := proactiveNumberToken.FindAllString(a, -1)
	bTokens := proactiveNumberToken.FindAllString(b, -1)
	if len(aTokens) != len(bTokens) {
		return false
	}
	for i := range aTokens {
		aTokens[i] = strings.ReplaceAll(aTokens[i], " ", "")
		bTokens[i] = strings.ReplaceAll(bTokens[i], " ", "")
	}
	sort.Strings(aTokens)
	sort.Strings(bTokens)
	for i := range aTokens {
		if aTokens[i] != bTokens[i] {
			return false
		}
	}
	return true
}
