package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// WorkingMemory provides a Redis-backed short-term memory for the current
// conversation. It stores compressed conversation summaries per user so that
// context assembly can inject recent conversation state without re-reading
// the full history from PostgreSQL.
//
// TTL is 30 minutes — long enough to cover a conversation session, short
// enough to avoid stale state leaking into new sessions.
const (
	workingMemoryTTL   = 30 * time.Minute
	workingMemoryKeyPrefix = "miriam:wm:"
	workingMemoryMaxChars  = 500
)

// WorkingMemoryEntry is the cached conversation state for a user.
type WorkingMemoryEntry struct {
	Summary       string    `json:"summary"`
	Topic         string    `json:"topic"`
	MessageCount  int       `json:"message_count"`
	LastExchangeAt time.Time `json:"last_exchange_at"`
	ActiveThread  string    `json:"active_thread"`
}

// GetSummary returns the conversation summary (satisfies core.WorkingMemorySnapshot).
func (e *WorkingMemoryEntry) GetSummary() string { return e.Summary }

// GetTopic returns the current conversation topic.
func (e *WorkingMemoryEntry) GetTopic() string { return e.Topic }

// GetMessageCount returns the number of messages in this session.
func (e *WorkingMemoryEntry) GetMessageCount() int { return e.MessageCount }

// WorkingMemoryStore handles read/write to Redis for conversation working memory.
type WorkingMemoryStore struct {
	cache  cache.RedisClient
	logger *zap.Logger
}

// NewWorkingMemoryStore creates a new working memory store.
func NewWorkingMemoryStore(cache cache.RedisClient, logger *zap.Logger) *WorkingMemoryStore {
	return &WorkingMemoryStore{cache: cache, logger: logger}
}

// Get retrieves the current working memory for a user. Returns nil if none exists
// or if Redis is unavailable (fail-open).
func (w *WorkingMemoryStore) Get(ctx context.Context, userID uuid.UUID) *WorkingMemoryEntry {
	if w.cache == nil {
		return nil
	}
	key := w.key(userID)
	var entry WorkingMemoryEntry
	if err := w.cache.Get(ctx, key, &entry); err != nil {
		return nil
	}
	return &entry
}

// Save stores or updates the working memory for a user.
func (w *WorkingMemoryStore) Save(ctx context.Context, userID uuid.UUID, entry *WorkingMemoryEntry) error {
	if w.cache == nil {
		return nil
	}
	entry.LastExchangeAt = time.Now().UTC()
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal working memory: %w", err)
	}
	if len(data) > workingMemoryMaxChars*2 {
		data = data[:workingMemoryMaxChars*2]
	}
	return w.cache.Set(ctx, w.key(userID), string(data), workingMemoryTTL)
}

// AppendExchange updates the working memory with a new conversation turn.
// It compresses the exchange into the existing summary, keeping the working
// memory compact while preserving conversation flow.
func (w *WorkingMemoryStore) AppendExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) {
	entry := w.Get(ctx, userID)
	if entry == nil {
		entry = &WorkingMemoryEntry{}
	}

	entry.MessageCount++

	// Compress the latest exchange into a brief note.
	userBrief := truncate(userMsg, 80)
	assistantBrief := truncate(assistantMsg, 80)

	// Build updated summary: keep existing context, append latest exchange.
	var parts []string
	if entry.Summary != "" {
		parts = append(parts, entry.Summary)
	}
	parts = append(parts, fmt.Sprintf("User: %s / Miriam: %s", userBrief, assistantBrief))

	// Keep summary under the character limit.
	joined := strings.Join(parts, ". ")
	if len(joined) > workingMemoryMaxChars {
		joined = joined[len(joined)-workingMemoryMaxChars:]
	}

	entry.Summary = joined

	// Detect topic from the latest user message.
	entry.Topic = extractTopic(userMsg)

	// Maintain a short active-thread note: the user's latest unresolved goal,
	// proposal, or question. This lets Miriam keep continuity across short
	// follow-up turns without re-reading the full conversation.
	entry.ActiveThread = buildActiveThread(userMsg, assistantBrief)

	if err := w.Save(ctx, userID, entry); err != nil {
		w.logger.Debug("failed to save working memory", zap.Error(err))
	}
}

// Clear removes the working memory for a user.
func (w *WorkingMemoryStore) Clear(ctx context.Context, userID uuid.UUID) error {
	if w.cache == nil {
		return nil
	}
	return w.cache.Del(ctx, w.key(userID))
}

func (w *WorkingMemoryStore) key(userID uuid.UUID) string {
	return workingMemoryKeyPrefix + userID.String()
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "..."
}

// extractTopic does lightweight topic detection from a user message.
func extractTopic(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "budget") || strings.Contains(lower, "spending"):
		return "budget"
	case strings.Contains(lower, "save") || strings.Contains(lower, "savings") || strings.Contains(lower, "stash"):
		return "savings"
	case strings.Contains(lower, "invest") || strings.Contains(lower, "portfolio"):
		return "investing"
	case strings.Contains(lower, "salary") || strings.Contains(lower, "income") || strings.Contains(lower, "pay"):
		return "income"
	case strings.Contains(lower, "bill") || strings.Contains(lower, "subscription"):
		return "bills"
	case strings.Contains(lower, "transfer") || strings.Contains(lower, "send") || strings.Contains(lower, "move"):
		return "transfers"
	case strings.Contains(lower, "goal"):
		return "goals"
	case strings.Contains(lower, "card") || strings.Contains(lower, "spend"):
		return "spending"
	default:
		return "general"
	}
}

// buildActiveThread extracts a compact continuity note from the latest turn.
// It prefers the user's stated goal or request, falls back to Miriam's last
// proposal, and keeps the note short enough for prompt context.
func buildActiveThread(userMsg, assistantBrief string) string {
	thread := normalizeActiveThreadCandidate(userMsg)
	if thread == "" {
		thread = normalizeActiveThreadCandidate(assistantBrief)
	}
	return thread
}

func normalizeActiveThreadCandidate(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "i'll "), strings.Contains(lower, "i can "), strings.Contains(lower, "let me "), strings.Contains(lower, "want me to "):
		return trimmed
	case strings.Contains(lower, "goal"), strings.Contains(lower, "saving for "), strings.Contains(lower, "save for "):
		return trimmed
	case strings.Contains(lower, "automation"), strings.Contains(lower, "every "), strings.Contains(lower, "recurring"):
		return trimmed
	case strings.Contains(lower, "transfer"), strings.Contains(lower, "move "), strings.Contains(lower, "send "):
		return trimmed
	}
	if len([]rune(trimmed)) <= 90 {
		return trimmed
	}
	return string([]rune(trimmed)[:88]) + "..."
}
