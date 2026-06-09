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

// MemoryStore is the persistence interface for Miriam's long-term memory.
type MemoryStore interface {
	SaveFact(ctx context.Context, fact *entities.MiriamUserFact, supersedes *uuid.UUID) error
	GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error)
	GetActiveFactsByCategory(ctx context.Context, userID uuid.UUID, category string) ([]*entities.MiriamUserFact, error)
	ConfirmFact(ctx context.Context, factID, userID uuid.UUID) error
	DeleteFact(ctx context.Context, factID, userID uuid.UUID) error
	GetToneProfile(ctx context.Context, userID uuid.UUID) (*entities.MiriamToneProfile, error)
	UpsertToneProfile(ctx context.Context, userID uuid.UUID, formality, directness, warmth, humor, brevity decimal.Decimal, preferredName, languageStyle, localeStyle string) error
	SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error
	// Staleness
	DecayStaleFacts(ctx context.Context, staleDuration time.Duration, decayAmount decimal.Decimal) (int64, error)
	ExpireLowConfidenceFacts(ctx context.Context, threshold decimal.Decimal) (int64, error)
	// Embeddings
	SetFactEmbedding(ctx context.Context, factID uuid.UUID, embedding []float32) error
	FindSimilarFacts(ctx context.Context, userID uuid.UUID, embedding []float32, category string, limit int) ([]entities.SimilarFact, error)
	// Summary
	GetMemorySummary(ctx context.Context, userID uuid.UUID) (*entities.MiriamMemorySummary, error)
	UpsertMemorySummary(ctx context.Context, userID uuid.UUID, summary string, factCount int) error
	// Batch
	GetAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// MemoryService handles fact extraction and tone calibration after each conversation exchange.
type MemoryService struct {
	store      MemoryStore
	aiProvider infraai.AIProvider
	embedder   Embedder
	logger     *zap.Logger
}

func NewMemoryService(store MemoryStore, aiProvider infraai.AIProvider, logger *zap.Logger) *MemoryService {
	return &MemoryService{store: store, aiProvider: aiProvider, logger: logger}
}

// SetEmbedder enables semantic dedup (optional — works without it).
func (m *MemoryService) SetEmbedder(e Embedder) { m.embedder = e }

// factExtractionPrompt instructs the LLM to extract structured facts from a conversation exchange.
const factExtractionPrompt = `You are a memory extraction system for a financial coaching AI named Miriam. Analyze the user's message and extract any personal facts worth remembering long-term.

Extract facts in these categories:
- goal: financial goals, savings targets, things they want to buy/achieve
- life_event: job changes, moves, marriage, baby, graduation, etc.
- preference: how they like to be addressed, communication preferences
- habit: spending patterns, routines they mention
- fear: financial anxieties, things they worry about
- family: mentions of partner, kids, parents, dependents
- work: job, industry, employer, freelance status, side hustles
- location: city, country, whether they moved recently
- identity: name, age, pronouns, cultural background they share
- financial_behavior: how they relate to money (impulsive, cautious, etc.)
- income_pattern: irregular income, seasonal work, bonuses, commissions
- deposit_cadence: how often money hits their account (weekly, biweekly, monthly, irregular)
- salary_day: specific day of month or day of week they get paid
- freelance_pattern: project-based income, client payment timelines, invoicing habits
- family_support: remittances, supporting parents/siblings, child support, alimony
- currency_context: which currencies they earn, spend, or save in; multi-currency lifestyle
- risk_preference: conservative vs aggressive with investments, willingness to try new products
- stash_behavior: how they treat savings (set-and-forget, frequent adjuster, target-saver)

Also detect the user's communication style for tone calibration:
- formality: 0.0 (very casual, slang, abbreviations) to 1.0 (formal, proper grammar)
- directness: 0.0 (indirect, hedging) to 1.0 (blunt, to the point)
- warmth: 0.0 (transactional) to 1.0 (friendly, uses greetings/emojis)
- humor: 0.0 (serious) to 1.0 (jokes, playful)
- brevity: 0.0 (verbose, long messages) to 1.0 (very short messages)
- language_style: "standard", "casual", "formal", or "pidgin_mix"
- preferred_name: if the user mentions their name or asks to be called something
- locale_style: "global", "nigeria", "west_africa", "diaspora_us", "diaspora_uk", "europe", or "formal_global" — infer from their location, currency usage, and cultural references

Return ONLY valid JSON with this structure (no markdown, no explanation):
{
  "facts": [
    {"category": "goal", "fact": "wants to save for a car by December", "confidence": 0.9}
  ],
  "tone": {
    "formality": 0.3,
    "directness": 0.7,
    "warmth": 0.6,
    "humor": 0.4,
    "brevity": 0.5,
    "language_style": "casual",
    "preferred_name": "",
    "locale_style": "global"
  }
}

Rules:
- Only extract facts the user explicitly stated or strongly implied. Do not invent.
- If no facts are present, return empty "facts" array.
- Always return tone analysis based on the user's writing style.
- Keep facts concise (under 100 chars each).
- Confidence: 1.0 = user explicitly stated it, 0.7 = strongly implied, 0.5 = loosely implied.`

type extractionResult struct {
	Facts []struct {
		Category   string  `json:"category"`
		Fact       string  `json:"fact"`
		Confidence float64 `json:"confidence"`
	} `json:"facts"`
	Tone struct {
		Formality     float64 `json:"formality"`
		Directness    float64 `json:"directness"`
		Warmth        float64 `json:"warmth"`
		Humor         float64 `json:"humor"`
		Brevity       float64 `json:"brevity"`
		LanguageStyle string  `json:"language_style"`
		PreferredName string  `json:"preferred_name"`
		LocaleStyle   string  `json:"locale_style"`
	} `json:"tone"`
}

// ProcessExchange extracts facts and calibrates tone from a user message + assistant response.
// Runs asynchronously — errors are logged, not returned to the user.
func (m *MemoryService) ProcessExchange(userID uuid.UUID, userMessage, assistantResponse string) {
	go func() {
		processCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		result, err := m.extractFromExchange(processCtx, userMessage, assistantResponse)
		if err != nil {
			m.logger.Warn("memory extraction failed", zap.Error(err), zap.String("user_id", userID.String()))
			return
		}

		m.saveFacts(processCtx, userID, result)
		m.updateTone(processCtx, userID, result)
	}()
}

func (m *MemoryService) extractFromExchange(ctx context.Context, userMessage, assistantResponse string) (*extractionResult, error) {
	// Truncate inputs to prevent token overflow on the extraction call.
	// Facts come from the user message; the assistant response provides context only.
	if len(userMessage) > 2000 {
		userMessage = userMessage[:2000]
	}
	if len(assistantResponse) > 1000 {
		assistantResponse = assistantResponse[:1000]
	}

	prompt := fmt.Sprintf("User message: %s\n\nAssistant response: %s", userMessage, assistantResponse)

	resp, err := m.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: factExtractionPrompt,
		Messages:     []infraai.Message{{Role: "user", Content: prompt}},
		MaxTokens:    500,
		Temperature:  infraai.Float64(0.1),
	})
	if err != nil {
		return nil, fmt.Errorf("extraction LLM call: %w", err)
	}

	if resp.TokensUsed > 0 {
		m.logger.Debug("memory extraction tokens", zap.Int("tokens", resp.TokensUsed), zap.String("provider", resp.Provider))
	}

	content := strings.TrimSpace(resp.Content)
	// Strip markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result extractionResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse extraction result: %w (raw: %s)", err, content[:min(len(content), 200)])
	}
	return &result, nil
}

func (m *MemoryService) saveFacts(ctx context.Context, userID uuid.UUID, result *extractionResult) {
	validCategories := map[string]bool{
		"goal": true, "life_event": true, "preference": true, "habit": true,
		"fear": true, "family": true, "work": true, "location": true,
		"identity": true, "financial_behavior": true,
		"income_pattern": true, "deposit_cadence": true, "salary_day": true,
		"freelance_pattern": true, "family_support": true, "currency_context": true,
		"risk_preference": true, "stash_behavior": true,
		"conversation_moment": true,
	}

	for _, f := range result.Facts {
		if f.Fact == "" || f.Confidence < 0.5 {
			continue
		}
		// Truncate fact text to keep memory context compact
		factText := f.Fact
		if len(factText) > 200 {
			factText = factText[:200]
		}
		if !validCategories[f.Category] {
			continue
		}

		// Check for existing facts in the same category that this might supersede
		existing, err := m.store.GetActiveFactsByCategory(ctx, userID, f.Category)
		if err != nil {
			m.logger.Warn("failed to check existing facts", zap.Error(err))
		}

		var supersedes *uuid.UUID

		// Semantic dedup: use embeddings if available, fall back to exact match
		if m.embedder != nil {
			emb, embErr := m.embedder.Embed(ctx, factText)
			if embErr == nil && len(emb) > 0 {
				similar, _ := m.store.FindSimilarFacts(ctx, userID, emb, f.Category, 1)
				if len(similar) > 0 && similar[0].Distance < 0.3 {
					// Distance < 0.3 = semantically similar enough to be the same fact
					if similar[0].Distance < 0.15 {
						// Very similar — just confirm the existing fact
						if err := m.store.ConfirmFact(ctx, similar[0].ID, userID); err != nil {
							m.logger.Warn("failed to confirm fact", zap.Error(err))
						}
						goto nextFact
					}
					// Moderately similar — supersede for single-value categories
					if isSingleValueCategory(f.Category) {
						supersedes = &similar[0].ID
					}
				}
				// Save fact with embedding
				fact := &entities.MiriamUserFact{
					UserID:     userID,
					Category:   f.Category,
					Fact:       factText,
					Source:     entities.FactSourceConversation,
					Confidence: decimal.NewFromFloat(f.Confidence),
				}
				if err := m.store.SaveFact(ctx, fact, supersedes); err != nil {
					m.logger.Warn("failed to save fact", zap.Error(err), zap.String("category", f.Category))
				} else {
					_ = m.store.SetFactEmbedding(ctx, fact.ID, emb)
				}
				goto nextFact
			}
		}

		// Fallback: exact match dedup
		for _, ex := range existing {
			if strings.EqualFold(ex.Fact, factText) {
				if err := m.store.ConfirmFact(ctx, ex.ID, userID); err != nil {
					m.logger.Warn("failed to confirm fact", zap.Error(err))
				}
				goto nextFact
			}
		}

		if len(existing) > 0 && isSingleValueCategory(f.Category) {
			supersedes = &existing[0].ID
		}

		{
			fact := &entities.MiriamUserFact{
				UserID:     userID,
				Category:   f.Category,
				Fact:       factText,
				Source:     entities.FactSourceConversation,
				Confidence: decimal.NewFromFloat(f.Confidence),
			}
			if err := m.store.SaveFact(ctx, fact, supersedes); err != nil {
				m.logger.Warn("failed to save fact", zap.Error(err), zap.String("category", f.Category))
			}
		}
	nextFact:
	}
}

func isSingleValueCategory(cat string) bool {
	switch cat {
	case "work", "location", "salary_day", "currency_context", "risk_preference":
		return true
	}
	return false
}

func (m *MemoryService) updateTone(ctx context.Context, userID uuid.UUID, result *extractionResult) {
	t := result.Tone

	// Validate language_style against DB CHECK constraint
	validStyles := map[string]bool{"standard": true, "casual": true, "formal": true, "pidgin_mix": true}
	langStyle := t.LanguageStyle
	if !validStyles[langStyle] {
		langStyle = "standard"
	}

	// Validate locale_style against DB CHECK constraint
	validLocales := map[string]bool{"global": true, "nigeria": true, "west_africa": true, "diaspora_us": true, "diaspora_uk": true, "europe": true, "formal_global": true}
	localeStyle := t.LocaleStyle
	if !validLocales[localeStyle] {
		localeStyle = "global"
	}

	// Truncate preferred_name to fit VARCHAR(100)
	preferredName := t.PreferredName
	if len(preferredName) > 100 {
		preferredName = preferredName[:100]
	}

	err := m.store.UpsertToneProfile(ctx, userID,
		clampDecimal(t.Formality),
		clampDecimal(t.Directness),
		clampDecimal(t.Warmth),
		clampDecimal(t.Humor),
		clampDecimal(t.Brevity),
		preferredName,
		langStyle,
		localeStyle,
	)
	if err != nil {
		m.logger.Warn("failed to update tone profile", zap.Error(err))
	}
}

func clampDecimal(v float64) decimal.Decimal {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return decimal.NewFromFloat(v)
}

// BuildMemoryContext assembles all remembered facts into a system prompt injection.
func (m *MemoryService) BuildMemoryContext(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	facts, err := m.store.GetActiveFacts(fetchCtx, userID)
	if err != nil || len(facts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[MIRIAM'S MEMORY — things you know about this user from past conversations. Use these naturally, don't list them back. Reference them when relevant to make the user feel known.]\n")

	grouped := make(map[string][]string)
	for _, f := range facts {
		grouped[f.Category] = append(grouped[f.Category], f.Fact)
	}

	categoryLabels := map[string]string{
		"goal":                "Goals",
		"life_event":          "Life events",
		"preference":          "Preferences",
		"habit":               "Habits",
		"fear":                "Concerns",
		"family":              "Family",
		"work":                "Work",
		"location":            "Location",
		"identity":            "Identity",
		"financial_behavior":  "Money personality",
		"income_pattern":      "Income pattern",
		"deposit_cadence":     "Deposit cadence",
		"salary_day":          "Salary day",
		"freelance_pattern":   "Freelance pattern",
		"family_support":      "Family support",
		"currency_context":    "Currency context",
		"risk_preference":     "Risk preference",
		"stash_behavior":      "Stash behavior",
		"conversation_moment": "Past moments",
	}

	for cat, label := range categoryLabels {
		if items, ok := grouped[cat]; ok {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", label, strings.Join(items, "; ")))
		}
	}

	return sb.String()
}

// BuildToneContext returns a tone instruction for the system prompt.
func (m *MemoryService) BuildToneContext(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	profile, err := m.store.GetToneProfile(fetchCtx, userID)
	if err != nil || profile == nil {
		return ""
	}

	// Only apply tone calibration after enough samples
	if profile.SampleCount < 3 {
		return ""
	}

	var parts []string

	if profile.PreferredName != "" {
		parts = append(parts, fmt.Sprintf("Call this user %q", profile.PreferredName))
	}

	if profile.Formality.LessThan(decimal.NewFromFloat(0.3)) {
		parts = append(parts, "Use very casual language, slang is fine")
	} else if profile.Formality.GreaterThan(decimal.NewFromFloat(0.7)) {
		parts = append(parts, "Use more formal, polished language")
	}

	if profile.Directness.GreaterThan(decimal.NewFromFloat(0.7)) {
		parts = append(parts, "Be very direct and blunt")
	} else if profile.Directness.LessThan(decimal.NewFromFloat(0.3)) {
		parts = append(parts, "Be gentle and indirect in delivery")
	}

	if profile.Warmth.GreaterThan(decimal.NewFromFloat(0.7)) {
		parts = append(parts, "Be extra warm and encouraging")
	}

	if profile.Humor.GreaterThan(decimal.NewFromFloat(0.7)) {
		parts = append(parts, "Use more humor and playfulness")
	} else if profile.Humor.LessThan(decimal.NewFromFloat(0.2)) {
		parts = append(parts, "Keep it serious, minimal humor")
	}

	if profile.Brevity.GreaterThan(decimal.NewFromFloat(0.7)) {
		parts = append(parts, "Keep responses very short and punchy")
	} else if profile.Brevity.LessThan(decimal.NewFromFloat(0.3)) {
		parts = append(parts, "This user likes detailed explanations")
	}

	if profile.LanguageStyle == "pidgin_mix" {
		parts = append(parts, "Mix in Nigerian Pidgin naturally when it fits")
	}

	switch profile.LocaleStyle {
	case "nigeria":
		parts = append(parts, "Use Naira (NGN) references and Nigerian financial context naturally")
	case "west_africa":
		parts = append(parts, "Use West African financial context and references")
	case "diaspora_us":
		parts = append(parts, "Reference USD and US financial systems; user is likely in the diaspora")
	case "diaspora_uk":
		parts = append(parts, "Reference GBP and UK financial systems; user is likely in the diaspora")
	case "europe":
		parts = append(parts, "Reference EUR and European financial context")
	case "formal_global":
		parts = append(parts, "Use globally-appropriate formal financial language")
	}

	if len(parts) == 0 {
		return ""
	}

	return "[TONE CALIBRATION — adapt your style for this user: " + strings.Join(parts, ". ") + ".]"
}

// SetPersonalityMode updates the user's chosen personality mode.
func (m *MemoryService) SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error {
	return m.store.SetPersonalityMode(ctx, userID, mode)
}

// --- Memory Summarization ---

const memorySummarizationPrompt = `Compress these user facts into a concise narrative paragraph (under 150 words). Write in third person. Include: who they are, what they do, their financial goals, habits, concerns, and personality. This will be used as context for a financial coaching AI.

Do NOT include any account numbers, passwords, or sensitive financial details. Focus on personality, goals, and behavioral patterns.`

// SummarizeMemory compresses all active facts into a narrative for users with many facts.
func (m *MemoryService) SummarizeMemory(ctx context.Context, userID uuid.UUID) error {
	facts, err := m.store.GetActiveFacts(ctx, userID)
	if err != nil || len(facts) < 10 {
		return err // Only summarize when there are enough facts
	}

	var sb strings.Builder
	for _, f := range facts {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", f.Category, f.Fact))
	}

	resp, err := m.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: memorySummarizationPrompt,
		Messages:     []infraai.Message{{Role: "user", Content: sb.String()}},
		MaxTokens:    300,
		Temperature:  infraai.Float64(0.2),
	})
	if err != nil {
		return fmt.Errorf("summarization LLM call: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return nil
	}

	return m.store.UpsertMemorySummary(ctx, userID, summary, len(facts))
}

// BuildMemoryContextWithSummary uses the summary when available, falling back to individual facts.
func (m *MemoryService) BuildMemoryContextWithSummary(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Try summary first (more compact for users with many facts)
	if summary, err := m.store.GetMemorySummary(fetchCtx, userID); err == nil && summary != nil && summary.Summary != "" {
		return fmt.Sprintf("[MIRIAM'S MEMORY — what you know about this user. Use naturally, never list back.]\n%s", summary.Summary)
	}

	// Fall back to individual facts
	return m.BuildMemoryContext(ctx, userID)
}

// --- Memory Decay ---

// RunDecay reduces confidence of stale facts and expires very low confidence ones.
func (m *MemoryService) RunDecay(ctx context.Context) error {
	// Decay facts not confirmed in 90 days by 0.15
	decayed, err := m.store.DecayStaleFacts(ctx, 90*24*time.Hour, decimal.NewFromFloat(0.15))
	if err != nil {
		return err
	}
	// Expire facts that have decayed below 0.2
	expired, err := m.store.ExpireLowConfidenceFacts(ctx, decimal.NewFromFloat(0.2))
	if err != nil {
		return err
	}
	if decayed > 0 || expired > 0 {
		m.logger.Info("memory decay complete", zap.Int64("decayed", decayed), zap.Int64("expired", expired))
	}
	return nil
}

// --- User-Facing Memory Controls ---

// GetActiveFacts returns all active facts for a user (satisfies miriam.MemoryReader).
func (m *MemoryService) GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error) {
	return m.store.GetActiveFacts(ctx, userID)
}

// ListUserFacts returns all active facts for a user (for "what do you know about me?").
func (m *MemoryService) ListUserFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error) {
	return m.store.GetActiveFacts(ctx, userID)
}

// ForgetFact deletes a specific fact (for "forget that").
func (m *MemoryService) ForgetFact(ctx context.Context, userID, factID uuid.UUID) error {
	return m.store.DeleteFact(ctx, factID, userID)
}

// ForgetCategory deletes all facts in a category (for "forget everything about my work").
func (m *MemoryService) ForgetCategory(ctx context.Context, userID uuid.UUID, category string) error {
	facts, err := m.store.GetActiveFactsByCategory(ctx, userID, category)
	if err != nil {
		return err
	}
	for _, f := range facts {
		if err := m.store.DeleteFact(ctx, f.ID, userID); err != nil {
			m.logger.Warn("failed to delete fact in category", zap.Error(err))
		}
	}
	return nil
}

// --- Transaction Pattern → Memory Facts ---

// ListActiveUserIDs returns all user IDs with active memory facts.
func (m *MemoryService) ListActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	return m.store.GetAllActiveUserIDs(ctx)
}

// SaveTransactionPattern converts a detected transaction pattern into a memory fact.
func (m *MemoryService) SaveTransactionPattern(ctx context.Context, userID uuid.UUID, pattern, category string, confidence float64) error {
	// Check for existing similar fact
	existing, _ := m.store.GetActiveFactsByCategory(ctx, userID, category)
	for _, ex := range existing {
		if strings.EqualFold(ex.Fact, pattern) {
			return m.store.ConfirmFact(ctx, ex.ID, userID)
		}
	}

	var supersedes *uuid.UUID
	if len(existing) > 0 && isSingleValueCategory(category) {
		supersedes = &existing[0].ID
	}

	fact := &entities.MiriamUserFact{
		UserID:     userID,
		Category:   category,
		Fact:       pattern,
		Source:     entities.FactSourceTransactionPattern,
		Confidence: decimal.NewFromFloat(confidence),
	}
	return m.store.SaveFact(ctx, fact, supersedes)
}
