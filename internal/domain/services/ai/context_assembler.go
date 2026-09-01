package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	aicontext "github.com/rail-service/rail_service/internal/domain/services/ai/context"
	"github.com/rail-service/rail_service/internal/domain/services/ai/channel"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ContextAssemblyOpts controls what gets assembled.
type ContextAssemblyOpts struct {
	ToneMode  string    // per-request tone (gentle/hard)
	Message   string    // user message (used for supermemory relevance)
	ConvID    uuid.UUID // conversation — used to look up staged pending actions
	FromVoice bool      // message was transcribed from a voice note
}

// channelContextKey is the context key for channel context.
type channelContextKey struct{}

// WithChannelContext attaches channel rendering context to the request context.
func WithChannelContext(ctx context.Context, channelCtx *channel.ChannelContext) context.Context {
	return context.WithValue(ctx, channelContextKey{}, channelCtx)
}

// GetChannelContext retrieves channel context from the request context.
func GetChannelContext(ctx context.Context) (*channel.ChannelContext, bool) {
	v := ctx.Value(channelContextKey{})
	if v == nil {
		return nil, false
	}
	c, ok := v.(*channel.ChannelContext)
	return c, ok
}

// assembleContext runs all context lookups in parallel and returns system messages
// to prepend to the conversation. Used by both streaming and non-streaming chat paths.
// Total assembly is capped at ~1.5s.
func (o *AgentAdapter) assembleContext(ctx context.Context, userID uuid.UUID, opts ContextAssemblyOpts) []ai.Message {
	builder := aicontext.NewBuilder(o.BuildContextDeps())
	messages := builder.Assemble(ctx, userID, aicontext.ContextAssemblyOpts{
		ToneMode:  opts.ToneMode,
		Message:   opts.Message,
		ConvID:    opts.ConvID,
		FromVoice: opts.FromVoice,
	})

	channelCtx := o.buildChannelContext(ctx, userID, opts.Message)
	if channelCtx != "" {
		messages = append(messages, ai.Message{
			Role:    "system",
			Content: channelCtx,
		})
	}

	return messages
}

// pendingActionContext returns guidance about a staged-but-unconfirmed action.
// Kept in root because it reads from o.pending which is owned by AgentAdapter.
func (o *AgentAdapter) pendingActionContext(ctx context.Context, convID uuid.UUID) string {
	if o.pending == nil || convID == uuid.Nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	action := o.pending.Get(fetchCtx, convID)
	if action == nil || action.IsExpired() {
		return ""
	}
	label := action.Description
	if label == "" {
		label = action.Action
	}
	return fmt.Sprintf(
		"[PENDING ACTION — %q (%s) is already staged and awaiting in-app confirmation. If the user affirms (\"yeah\", \"do it\", \"confirm\"), call confirm_action — do NOT call %s again or you will double-stage it. If they decline or change their mind, call cancel_action. Never claim it executed until confirm_action succeeds.]",
		label, action.Action, action.Action,
	)
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
	text := "[ANOMALIES DETECTED — surface these proactively, leading with the most severe, when the conversation touches their money or they ask what's new. Be specific about amounts and merchants from the list. Don't derail unrelated or casual messages; never invent details beyond what's listed here.]"
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

// buildActiveThreadContext returns the latest unresolved goal or proposal from
// working memory so Miriam can maintain continuity across short follow-up turns.
func (o *AgentAdapter) buildActiveThreadContext(ctx context.Context, userID uuid.UUID) string {
	if o.workingMemory == nil || userID == uuid.Nil {
		return ""
	}
	entry := o.workingMemory.Get(ctx, userID)
	if entry == nil || strings.TrimSpace(entry.ActiveThread) == "" {
		return ""
	}
	return fmt.Sprintf("[ACTIVE THREAD — keep continuity with this unresolved thread: %s]", strings.TrimSpace(entry.ActiveThread))
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

// buildChannelContext injects platform capability awareness so Miriam adapts
// her responses to the current chat platform (iMessage, WhatsApp, Telegram, SMS).
// This ensures she stays within platform limits (max bubbles, chars, available affordances)
// and uses the appropriate tone and rendering style.
func (o *AgentAdapter) buildChannelContext(ctx context.Context, userID uuid.UUID, message string) string {
	if o == nil {
		return ""
	}

	channelCtx, ok := ctx.Value(channelContextKey{}).(*channel.ChannelContext)
	if !ok || channelCtx == nil {
		return ""
	}

	caps := channelCtx.Capabilities
	return fmt.Sprintf(
		"[CHANNEL — %s. Max bubbles per reply: %d. Max chars per bubble: %d. Polls: %v. Quick replies: %v. Voice notes: %v. Image support: %v. Tone: %s.]",
		channelCtx.Platform,
		caps.MaxBubblesPerReply,
		caps.MaxCharsPerBubble,
		caps.SupportsPolls,
		caps.SupportsQuickReplies,
		caps.SupportsVoiceIn,
		caps.SupportsImageIn,
		caps.PreferredTone,
	)
}
