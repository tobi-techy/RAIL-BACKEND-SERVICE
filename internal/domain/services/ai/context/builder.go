package context

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
	ToneMode string
	Message  string
	ConvID   uuid.UUID
}

// Builder assembles per-turn system context for Miriam.
type Builder struct {
	deps *ContextDeps
}

// NewBuilder creates a context Builder.
func NewBuilder(deps *ContextDeps) *Builder {
	return &Builder{deps: deps}
}

// Assemble runs all context lookups in parallel and returns system messages.
// Total assembly is capped at ~1.5s.
func (b *Builder) Assemble(ctx context.Context, userID uuid.UUID, opts ContextAssemblyOpts) []ai.Message {
	if pa := b.pendingActionContext(ctx, opts.ConvID); pa != "" {
		return []ai.Message{{Role: "system", Content: pa}}
	}

	msgLen := len([]rune(strings.TrimSpace(opts.Message)))
	if msgLen < 15 && opts.Message != "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	const numSlots = 17
	results := make([]string, numSlots)

	g, gCtx := errgroup.WithContext(ctx)

	slotNames := [numSlots]string{
		"balance", "stash_lock", "financial_profile", "user_profile",
		"bank_statement", "memory", "personality", "user_time", "naira_context",
		"personal_memory", "anomalies", "working_memory", "financial_events",
		"enrichment_summary", "onboarding", "coaching_state", "active_thread",
	}

	buildSlot := func(slot int, name string, fn func() string) func() error {
		return func() error {
			defer func() {
				if r := recover(); r != nil && b.deps.Logger != nil {
					b.deps.Logger.Error("context builder panicked", zap.String("slot", name), zap.Any("panic", r), zap.String("user_id", userID.String()))
				}
			}()
			results[slot] = fn()
			return nil
		}
	}

	g.Go(buildSlot(0, slotNames[0], func() string { return b.buildBalanceContext(gCtx, userID) }))
	g.Go(buildSlot(1, slotNames[1], func() string { return b.buildStashLockContext(gCtx, userID) }))
	g.Go(buildSlot(2, slotNames[2], func() string { return b.buildFinancialProfileContext(gCtx, userID) }))
	g.Go(buildSlot(3, slotNames[3], func() string { return b.buildUserProfileContext(gCtx, userID) }))
	g.Go(buildSlot(4, slotNames[4], func() string {
		if b.deps.BankStatementBuildFn != nil {
			return b.deps.BankStatementBuildFn(gCtx, userID)
		}
		return ""
	}))
	g.Go(buildSlot(5, slotNames[5], func() string {
		if b.deps.BuildMemoryContextFn != nil {
			return b.deps.BuildMemoryContextFn(gCtx, userID, opts.Message)
		}
		return ""
	}))
	g.Go(buildSlot(6, slotNames[6], func() string {
		return b.buildConsolidatedPersonalityContext(gCtx, userID, opts.ToneMode)
	}))
	g.Go(buildSlot(7, slotNames[7], func() string { return b.buildUserTimeContext(gCtx, userID) }))
	g.Go(buildSlot(8, slotNames[8], func() string { return b.buildNairaContext(gCtx, userID) }))
	g.Go(buildSlot(9, slotNames[9], func() string { return b.buildPersonalMemoryContext(gCtx, userID, opts.Message) }))
	g.Go(buildSlot(10, slotNames[10], func() string { return b.buildAnomalyContext(gCtx, userID) }))
	g.Go(buildSlot(11, slotNames[11], func() string { return b.buildWorkingMemoryContext(gCtx, userID) }))
	g.Go(buildSlot(12, slotNames[12], func() string { return b.buildFinancialEventsContext(gCtx, userID) }))
	g.Go(buildSlot(13, slotNames[13], func() string { return b.buildEnrichmentSummaryContext(gCtx, userID) }))
	g.Go(buildSlot(14, slotNames[14], func() string { return b.buildOnboardingContext(gCtx, userID) }))
	g.Go(buildSlot(15, slotNames[15], func() string { return b.buildCoachingContext(gCtx, userID) }))
	g.Go(buildSlot(16, slotNames[16], func() string { return b.buildActiveThreadContext(gCtx, userID) }))

	_ = g.Wait()

	results[9] = dedupePersonalMemory(results[5], results[9])

	if ctx.Err() != nil && b.deps.Logger != nil {
		b.deps.Logger.Warn("context assembly hit timeout", zap.Error(ctx.Err()), zap.String("user_id", userID.String()))
	}

	messages := make([]ai.Message, 0, numSlots+2)
	for _, s := range results {
		if s != "" {
			messages = append(messages, ai.Message{Role: "system", Content: s})
		}
	}

	return messages
}

func (b *Builder) pendingActionContext(ctx context.Context, convID uuid.UUID) string {
	if b.deps.GetPendingActionFn == nil || convID == uuid.Nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	action := b.deps.GetPendingActionFn(fetchCtx, convID)
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
