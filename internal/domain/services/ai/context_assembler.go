package ai

import (
	"context"
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
func (o *Orchestrator) assembleContext(ctx context.Context, userID uuid.UUID, opts ContextAssemblyOpts) []ai.Message {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// 8 parallel slots — one per context source.
	// Results are indexed to maintain consistent ordering.
	const numSlots = 8
	results := make([]string, numSlots)

	g, gCtx := errgroup.WithContext(ctx)

	slotNames := [numSlots]string{
		"balance", "stash_lock", "financial_profile", "user_profile",
		"bank_statement", "memory", "personality", "user_time",
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

	_ = g.Wait()

	if ctx.Err() != nil && o.logger != nil {
		o.logger.Warn("context assembly hit timeout", zap.Error(ctx.Err()), zap.String("user_id", userID.String()))
	}

	// Assemble in stable order
	messages := make([]ai.Message, 0, numSlots+1)
	for _, s := range results {
		if s != "" {
			messages = append(messages, ai.Message{Role: "system", Content: s})
		}
	}

	// Supermemory: query-dependent, runs after the parallel batch.
	// Only for financial messages longer than a greeting.
	if o.supermemory != nil && userID != uuid.Nil && len(opts.Message) > 15 && looksFinancial(opts.Message) {
		smCtx, smCancel := context.WithTimeout(context.Background(), 2*time.Second)
		memories, smErr := o.supermemory.SearchMemory(smCtx, userID.String(), opts.Message, 8)
		smCancel()
		if smErr == nil && len(memories) > 0 {
			var sb strings.Builder
			sb.WriteString("[Personal financial memory (from uploaded bank statements, may be in NGN/local currency — do NOT conflate with USD Rail balances):\n")
			count := 0
			for _, m := range memories {
				if m.Similarity < 0.6 || count >= 6 {
					break
				}
				sb.WriteString("• ")
				sb.WriteString(m.Memory)
				sb.WriteString("\n")
				count++
			}
			sb.WriteString("]")
			if count > 0 {
				messages = append(messages, ai.Message{Role: "system", Content: sb.String()})
			}
		}
	}

	return messages
}
