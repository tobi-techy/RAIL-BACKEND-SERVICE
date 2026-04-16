package gameplay

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Hooks provides a single entry point for other services to trigger gameplay events.
// Injected via setter into funding, roundup, card, and onboarding services.
type Hooks struct {
	xp        *XPService
	streak    *StreakService
	challenge *ChallengeService
	logger    *zap.Logger
}

func NewHooks(xp *XPService, streak *StreakService, challenge *ChallengeService, logger *zap.Logger) *Hooks {
	return &Hooks{xp: xp, streak: streak, challenge: challenge, logger: logger}
}

// OnDeposit is called after a successful deposit
func (h *Hooks) OnDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID) {
	// XP: scale 10-50 based on amount
	xpAmount := 10
	if amount.GreaterThanOrEqual(decimal.NewFromInt(100)) {
		xpAmount = 30
	}
	if amount.GreaterThanOrEqual(decimal.NewFromInt(500)) {
		xpAmount = 50
	}
	if err := h.xp.AwardXP(ctx, userID, entities.XPEventDeposit, xpAmount, &depositID); err != nil {
		h.logger.Warn("gameplay: failed to award deposit XP", zap.Error(err))
	}

	// Streak
	if err := h.streak.RecordActivity(ctx, userID, entities.StreakTypeDeposit); err != nil {
		h.logger.Warn("gameplay: failed to record deposit streak", zap.Error(err))
	}

	// Challenge progress: increment deposit_count
	h.challenge.UpdateProgress(ctx, userID, "deposit_count", decimal.NewFromInt(1))
}

// OnFirstDeposit is called for a user's very first deposit
func (h *Hooks) OnFirstDeposit(ctx context.Context, userID uuid.UUID, depositID uuid.UUID) {
	if err := h.xp.AwardXP(ctx, userID, entities.XPEventFirstDeposit, XPFirstDeposit, &depositID); err != nil {
		h.logger.Warn("gameplay: failed to award first deposit XP", zap.Error(err))
	}
}

// OnRoundup is called after a roundup is triggered
func (h *Hooks) OnRoundup(ctx context.Context, userID uuid.UUID, txID uuid.UUID) {
	if err := h.xp.AwardXP(ctx, userID, entities.XPEventRoundup, XPRoundup, &txID); err != nil {
		h.logger.Warn("gameplay: failed to award roundup XP", zap.Error(err))
	}
	if err := h.streak.RecordActivity(ctx, userID, entities.StreakTypeRoundup); err != nil {
		h.logger.Warn("gameplay: failed to record roundup streak", zap.Error(err))
	}
	h.challenge.UpdateProgress(ctx, userID, "roundup_count", decimal.NewFromInt(1))
}

// OnFirstCardTransaction is called for a user's first card transaction
func (h *Hooks) OnFirstCardTransaction(ctx context.Context, userID uuid.UUID) {
	if err := h.xp.AwardXP(ctx, userID, entities.XPEventFirstCardTx, XPFirstCardTx, nil); err != nil {
		h.logger.Warn("gameplay: failed to award first card tx XP", zap.Error(err))
	}
	h.challenge.UpdateProgress(ctx, userID, "card_tx_count", decimal.NewFromInt(1))
}

// OnOnboardingComplete is called when a user finishes onboarding
func (h *Hooks) OnOnboardingComplete(ctx context.Context, userID uuid.UUID) {
	if err := h.challenge.AssignOnboardingChallenges(ctx, userID); err != nil {
		h.logger.Warn("gameplay: failed to assign onboarding challenges", zap.Error(err))
	}
}
