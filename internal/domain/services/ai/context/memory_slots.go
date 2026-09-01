package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	personalMemoryPrefix = "[PERSONAL MEMORY — things you've shared with this user that are relevant right now: "
	personalMemorySuffix = "]"
)

func (b *Builder) buildPersonalMemoryContext(ctx context.Context, userID uuid.UUID, message string) string {
	if b.deps.SearchMemoryRankedFn == nil || userID == uuid.Nil {
		return ""
	}
	msg := strings.TrimSpace(message)
	if len([]rune(msg)) < 12 {
		return ""
	}

	smCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()

	memories, err := b.deps.SearchMemoryRankedFn(smCtx, userID.String(), msg, 12)
	if err != nil || len(memories) == 0 {
		return ""
	}

	now := time.Now()
	tf := parseTimeframe(msg, now)
	ranked := rankMemoriesByRecency(memories, tf, now, 0.5)
	if len(ranked) == 0 {
		return ""
	}
	if len(ranked) > 6 {
		ranked = ranked[:6]
	}

	joined := strings.Join(ranked, " | ")
	if len(joined) > 1200 {
		joined = joined[:1200]
	}
	return personalMemoryPrefix + joined + personalMemorySuffix
}

func (b *Builder) buildAnomalyContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetAnomaliesFn == nil || userID == uuid.Nil {
		return ""
	}
	results, err := b.deps.GetAnomaliesFn(ctx, userID)
	if err != nil || len(results) == 0 {
		return ""
	}
	text := "[ANOMALIES DETECTED — surface these proactively, leading with the most severe, when the conversation touches their money or they ask what's new. Be specific about amounts and merchants from the list. Don't derail unrelated or casual messages; never invent details beyond what's listed here.]"
	for _, r := range results {
		text += fmt.Sprintf("\n[%s] %s — %s", strings.ToUpper(string(r.Severity)), r.Title, r.Description)
	}
	return text
}

func (b *Builder) buildWorkingMemoryContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetWorkingMemoryFn == nil || userID == uuid.Nil {
		return ""
	}
	entry := b.deps.GetWorkingMemoryFn(ctx, userID)
	if entry == nil || entry.Summary == "" {
		return ""
	}
	return fmt.Sprintf("[CONVERSATION STATE — recent context from this session: %s]", entry.Summary)
}

func (b *Builder) buildActiveThreadContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetActiveThreadFn == nil || userID == uuid.Nil {
		return ""
	}
	thread := b.deps.GetActiveThreadFn(ctx, userID)
	if strings.TrimSpace(thread) == "" {
		return ""
	}
	return fmt.Sprintf("[ACTIVE THREAD — keep continuity with this unresolved thread: %s]", thread)
}

func (b *Builder) buildFinancialEventsContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetFinancialEventsFn == nil || userID == uuid.Nil {
		return ""
	}
	return b.deps.GetFinancialEventsFn(ctx, userID)
}

func (b *Builder) buildEnrichmentSummaryContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetEnrichmentSummaryFn == nil || userID == uuid.Nil {
		return ""
	}
	summary, err := b.deps.GetEnrichmentSummaryFn(ctx, userID)
	if err != nil || summary == "" {
		return ""
	}
	return summary
}

func (b *Builder) buildUserTimeContext(_ context.Context, _ uuid.UUID) string {
	return fmt.Sprintf("[USER TIME — current UTC: %s]", time.Now().UTC().Format(time.RFC3339))
}

// dedupePersonalMemory removes content from slot 9 that's already in slot 5.
func dedupePersonalMemory(slot5, slot9 string) string {
	if slot5 == "" || slot9 == "" {
		return slot9
	}
	seen := make(map[string]bool)
	for _, p := range strings.Split(slot5, " | ") {
		seen[strings.TrimSpace(p)] = true
	}
	var kept []string
	for _, p := range strings.Split(slot9, " | ") {
		t := strings.TrimSpace(p)
		if t != "" && !seen[t] {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " | ")
}

// parseTimeframe and rankMemoriesByRecency are minimal placeholders.
func parseTimeframe(msg string, now time.Time) time.Time {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "last week"):
		return now.AddDate(0, 0, -7)
	case strings.Contains(lower, "last month"):
		return now.AddDate(0, -1, 0)
	}
	return time.Time{}
}

func rankMemoriesByRecency(memories []string, _ time.Time, _ time.Time, _ float64) []string {
	return memories
}
