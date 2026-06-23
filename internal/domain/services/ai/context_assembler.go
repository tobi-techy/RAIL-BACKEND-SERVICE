package ai

import (
	"context"
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
func (o *Orchestrator) assembleContext(ctx context.Context, userID uuid.UUID, opts ContextAssemblyOpts) []ai.Message {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	// 9 parallel slots — one per context source.
	// Results are indexed to maintain consistent ordering.
	const numSlots = 9
	results := make([]string, numSlots)

	g, gCtx := errgroup.WithContext(ctx)

	slotNames := [numSlots]string{
		"balance", "stash_lock", "financial_profile", "user_profile",
		"bank_statement", "memory", "personality", "user_time", "naira_context",
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
			return o.memory.BuildMemoryContextWithSummary(gCtx, userID)
		}
		return ""
	}))
	g.Go(buildSlot(6, slotNames[6], func() string {
		return o.buildConsolidatedPersonalityContext(gCtx, userID, opts.ToneMode)
	}))
	g.Go(buildSlot(7, slotNames[7], func() string { return o.buildUserTimeContext(gCtx, userID) }))
	g.Go(buildSlot(8, slotNames[8], func() string { return o.buildNairaContext(gCtx, userID) }))

	_ = g.Wait()

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

	// Supermemory moved to tool-call phase (V2) — no longer blocks context assembly.

	return messages
}
