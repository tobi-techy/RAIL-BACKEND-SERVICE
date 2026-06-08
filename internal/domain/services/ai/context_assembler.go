package ai

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
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

	// Slot 0: Balance
	g.Go(func() error {
		results[0] = o.buildBalanceContext(gCtx, userID)
		return nil
	})

	// Slot 1: Stash lock status
	g.Go(func() error {
		results[1] = o.buildStashLockContext(gCtx, userID)
		return nil
	})

	// Slot 2: Financial profile
	g.Go(func() error {
		results[2] = o.buildFinancialProfileContext(gCtx, userID)
		return nil
	})

	// Slot 3: User profile (name, country, age)
	g.Go(func() error {
		results[3] = o.buildUserProfileContext(gCtx, userID)
		return nil
	})

	// Slot 4: Bank statement context
	g.Go(func() error {
		if o.bankStatementCtx != nil {
			results[4] = o.bankStatementCtx.BuildContext(gCtx, userID)
		}
		return nil
	})

	// Slot 5: Long-term memory
	g.Go(func() error {
		if o.memory != nil {
			results[5] = o.memory.BuildMemoryContextWithSummary(gCtx, userID)
		}
		return nil
	})

	// Slot 6: Consolidated personality (phase + tone calibration + tone mode)
	g.Go(func() error {
		results[6] = o.buildConsolidatedPersonalityContext(gCtx, userID, opts.ToneMode)
		return nil
	})

	// Slot 7: User time context
	g.Go(func() error {
		results[7] = o.buildUserTimeContext(gCtx, userID)
		return nil
	})

	_ = g.Wait() // errors are swallowed — each builder returns "" on failure

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
