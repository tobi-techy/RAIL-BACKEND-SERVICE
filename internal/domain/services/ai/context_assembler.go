package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// ContextAssemblyOpts controls what gets assembled.
type ContextAssemblyOpts struct {
	ToneMode string // per-request tone (gentle/hard)
	Message  string // user message (used for supermemory relevance)
}

// assembleContext runs all context lookups in parallel and returns system messages
// to prepend to the conversation. Used by both streaming and non-streaming chat paths.
// All lookups have a 3s hard ceiling so total assembly never exceeds ~3s.
func (o *AgentAdapter) assembleContext(ctx context.Context, userID uuid.UUID, opts ContextAssemblyOpts) []ai.Message {
	// Short/casual messages (greetings, acks, "ok", "yeah") don't need context.
	// Saves ~10k tokens and ~1.5s latency on trivial turns.
	msgLen := len([]rune(strings.TrimSpace(opts.Message)))
	if msgLen < 15 && opts.Message != "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	// 14 parallel slots — one per context source.
	// Results are indexed to maintain consistent ordering.
	const numSlots = 14
	results := make([]string, numSlots)

	g, gCtx := errgroup.WithContext(ctx)

	slotNames := [numSlots]string{
		"balance", "stash_lock", "financial_profile", "user_profile",
		"bank_statement", "memory", "personality", "user_time", "naira_context",
		"personal_memory", "anomalies", "working_memory", "financial_events",
		"enrichment_summary",
	}

	buildSlot := func(slot int, name string, fn func() string) func() error {
		return func() error {
			defer func() {
				if r := recover(); r != nil && o.logger != nil {
					o.logger.Error("context builder panicked", zap.String("slot", name), zap.Any("panic", r), zap.String("user_id", userID.String()))
				}
			}()
			results[slot] = fn()
			return nil
		}
	}

	g.Go(buildSlot(0, slotNames[0], func() string { return o.buildBalanceContext(gCtx, userID) }))
	g.Go(buildSlot(1, slotNames[1], func() string { return o.buildStashLockContext(gCtx, userID) }))
	g.Go(buildSlot(2, slotNames[2], func() string { return o.buildFinancialProfileContext(gCtx, userID) }))
	g.Go(buildSlot(3, slotNames[3], func() string { return o.buildUserProfileContext(gCtx, userID) }))
	g.Go(buildSlot(4, slotNames[4], func() string {
		if o.bankStatementCtx != nil {
			return o.bankStatementCtx.BuildContext(gCtx, userID)
		}
		return ""
	}))
	g.Go(buildSlot(5, slotNames[5], func() string {
		if o.memory != nil {
			return o.memory.BuildMemoryContextWithSummary(gCtx, userID, opts.Message)
		}
		return ""
	}))
	g.Go(buildSlot(6, slotNames[6], func() string {
		return o.buildConsolidatedPersonalityContext(gCtx, userID, opts.ToneMode)
	}))
	g.Go(buildSlot(7, slotNames[7], func() string { return o.buildUserTimeContext(gCtx, userID) }))
	g.Go(buildSlot(8, slotNames[8], func() string { return o.buildNairaContext(gCtx, userID) }))
	g.Go(buildSlot(9, slotNames[9], func() string { return o.buildPersonalMemoryContext(gCtx, userID, opts.Message) }))
	g.Go(buildSlot(10, slotNames[10], func() string { return o.buildAnomalyContext(gCtx, userID) }))
	g.Go(buildSlot(11, slotNames[11], func() string { return o.buildWorkingMemoryContext(gCtx, userID) }))
	g.Go(buildSlot(12, slotNames[12], func() string { return o.buildFinancialEventsContext(gCtx, userID) }))
	g.Go(buildSlot(13, slotNames[13], func() string { return o.buildEnrichmentSummaryContext(gCtx, userID) }))

	_ = g.Wait()

	// Prevent the structured memory slot (5) and the recalled-memory slot (9) from
	// repeating each other; keep only what slot 9 adds beyond slot 5.
	results[9] = dedupePersonalMemory(results[5], results[9])

	if ctx.Err() != nil && o.logger != nil {
		o.logger.Warn("context assembly hit timeout", zap.Error(ctx.Err()), zap.String("user_id", userID.String()))
	}

	// Assemble in stable order
	messages := make([]ai.Message, 0, numSlots+2)
	for _, s := range results {
		if s != "" {
			messages = append(messages, ai.Message{Role: "system", Content: s})
		}
	}

	// Emotion detection: inject tone hint if user sounds non-neutral
	if hint := detectEmotion(opts.Message); hint != "" {
		messages = append(messages, ai.Message{Role: "system", Content: hint})
	}

	// Energy matching: tell Miriam how long/short to be based on user's message style
	if hint := detectEnergy(opts.Message); hint != "" {
		messages = append(messages, ai.Message{Role: "system", Content: hint})
	}

	return messages
}

// buildPersonalMemoryContext recalls the user's personal memory most relevant to
// their current message, so Miriam can reference past goals, worries, or habits
// naturally — the thing that makes her feel like a friend who remembers you.
// Skipped for trivial messages (greetings/acknowledgements) to save latency.
func (o *AgentAdapter) buildPersonalMemoryContext(ctx context.Context, userID uuid.UUID, message string) string {
	if o.supermemory == nil || userID == uuid.Nil {
		return ""
	}
	msg := strings.TrimSpace(message)
	if len([]rune(msg)) < 12 {
		return "" // greetings/acks aren't worth a memory lookup
	}

	smCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()

	// Over-fetch and rerank so recency weighting has candidates to work with; the
	// window (if any) is parsed from the message and applied client-side so undated
	// context is never dropped outright.
	memories, err := o.supermemory.SearchMemoryRanked(smCtx, userID.String(), msg, 12)
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

// buildAnomalyContext returns recent anomaly detections so Miriam can answer
// "what was that anomaly?" without needing a tool call.
func (o *AgentAdapter) buildAnomalyContext(ctx context.Context, userID uuid.UUID) string {
	if o.anomalyStore == nil || userID == uuid.Nil {
		return ""
	}
	results, err := o.anomalyStore.Get(ctx, userID)
	if err != nil || len(results) == 0 {
		return ""
	}
	text := "[ANOMALIES DETECTED — YOU MUST MENTION THESE PROACTIVELY. The user may not know about them yet. Lead with the most severe one, be specific about amounts and merchants.]"
	for _, r := range results {
		text += fmt.Sprintf("\n[%s] %s — %s", strings.ToUpper(string(r.Severity)), r.Title, r.Description)
	}
	return text
}

// buildWorkingMemoryContext returns the compressed conversation state from Redis
// so Miriam has continuity within a session without re-reading full history.
func (o *AgentAdapter) buildWorkingMemoryContext(ctx context.Context, userID uuid.UUID) string {
	if o.workingMemory == nil || userID == uuid.Nil {
		return ""
	}
	entry := o.workingMemory.Get(ctx, userID)
	if entry == nil || entry.Summary == "" {
		return ""
	}
	return fmt.Sprintf("[CONVERSATION STATE — recent context from this session: %s]", entry.Summary)
}

// buildFinancialEventsContext returns recent financial events from the timeline
// so Miriam can reference recent financial activity naturally.
func (o *AgentAdapter) buildFinancialEventsContext(ctx context.Context, userID uuid.UUID) string {
	if o.eventStore == nil || userID == uuid.Nil {
		return ""
	}
	return o.eventStore.BuildEventsContext(ctx, userID)
}

// buildEnrichmentSummaryContext returns a compact summary of the user's recent
// enriched transactions so Miriam has proactive awareness of spending patterns
// without needing a tool call.
func (o *AgentAdapter) buildEnrichmentSummaryContext(ctx context.Context, userID uuid.UUID) string {
	if o.enrichmentSummaryFn == nil || userID == uuid.Nil {
		return ""
	}
	summary, err := o.enrichmentSummaryFn(ctx, userID)
	if err != nil || summary == "" {
		return ""
	}
	return summary
}
