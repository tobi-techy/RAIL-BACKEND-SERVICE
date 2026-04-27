package gameplay

import (
	"context"
	"time"

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
	boosts    *BoostService
	points    *PointsService
	graceDay  *GraceDayService
	logger    *zap.Logger
}

func NewHooks(xp *XPService, streak *StreakService, challenge *ChallengeService, logger *zap.Logger) *Hooks {
	return &Hooks{xp: xp, streak: streak, challenge: challenge, logger: logger}
}

// SetBoosts wires the boost service after DI resolves
func (h *Hooks) SetBoosts(b *BoostService) { h.boosts = b }

// SetPoints wires the points service after DI resolves
func (h *Hooks) SetPoints(p *PointsService) { h.points = p }

// SetGraceDay wires the grace day service after DI resolves
func (h *Hooks) SetGraceDay(g *GraceDayService) { h.graceDay = g }

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

	// Rail Points: 5 points per deposit
	if h.points != nil {
		h.points.Earn(ctx, userID, 5, entities.PointSourceDeposit, &depositID, "+5 Rail Points: deposit")
	}
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

// OnCardTransaction is called after a card transaction completes
func (h *Hooks) OnCardTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, category string) {
	// Evaluate active boost
	if h.boosts != nil {
		completedBoostID, err := h.boosts.EvaluateTransaction(ctx, userID, amount, category)
		if err != nil {
			h.logger.Warn("gameplay: failed to evaluate boost", zap.Error(err))
		}
		if completedBoostID != nil {
			h.OnBoostComplete(ctx, userID, *completedBoostID)
		}
	}

	// Rail Points: 1 point per card transaction
	if h.points != nil {
		h.points.Earn(ctx, userID, 1, "card_transaction", nil, "+1 Rail Point: card use")
	}

	// Challenge: card_tx_count
	h.challenge.UpdateProgress(ctx, userID, "card_tx_count", decimal.NewFromInt(1))
}

// OnStashGrowth is called when stash balance increases (yield, manual save, etc.)
func (h *Hooks) OnStashGrowth(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) {
	if err := h.streak.RecordActivity(ctx, userID, entities.StreakTypeStashGrowth); err != nil {
		h.logger.Warn("gameplay: failed to record stash growth streak", zap.Error(err))
	}
	h.challenge.UpdateProgress(ctx, userID, "stash_growth", amount)

	// Rail Points: 2 points per stash growth event
	if h.points != nil {
		h.points.Earn(ctx, userID, 2, "stash_growth", nil, "+2 Rail Points: stash grew")
	}
}

// OnWithdrawal is called after a withdrawal. Breaks the no_panic_withdrawal streak.
func (h *Hooks) OnWithdrawal(ctx context.Context, userID uuid.UUID) {
	streaks, err := h.streak.GetUserStreaks(ctx, userID)
	if err != nil {
		return
	}
	for _, s := range streaks {
		if s.StreakType == entities.StreakTypeNoPanicWithdrawal && s.CurrentCount > 0 {
			// Try grace day first
			if h.graceDay != nil {
				saved, _ := h.graceDay.Consume(ctx, userID)
				if saved {
					return
				}
			}
			h.streak.ResetStreakByID(ctx, s.ID)
			return
		}
	}
}

// OnStreakDay is called daily for each active streak day (by the streak evaluator).
// Uses a deterministic sourceID for idempotency so re-runs don't double-award.
func (h *Hooks) OnStreakDay(ctx context.Context, userID uuid.UUID, streakType entities.StreakType, dayCount int) {
	// Deterministic ID: hash of userID + date + streakType for idempotency
	today := time.Now().UTC().Format("2006-01-02")
	sourceID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(userID.String()+today+string(streakType)))

	// Award XP for streak milestones
	if dayCount%7 == 0 {
		h.xp.AwardXP(ctx, userID, entities.XPEventStreakDay, XPStreakDay*3, &sourceID)
	} else {
		h.xp.AwardXP(ctx, userID, entities.XPEventStreakDay, XPStreakDay, &sourceID)
	}

	// Rail Points: 3 points per streak day
	if h.points != nil {
		h.points.Earn(ctx, userID, 3, entities.PointSourceStreakDay, &sourceID, "+3 Rail Points: streak day")
	}
}

// OnRingsClosed is called when all 3 rings are closed for a day
func (h *Hooks) OnRingsClosed(ctx context.Context, userID uuid.UUID, date time.Time) {
	// Deterministic ID for idempotency
	sourceID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(userID.String()+date.Format("2006-01-02")+"rings_closed"))

	// Rail Points: 10 points for closing all rings
	if h.points != nil {
		h.points.Earn(ctx, userID, 10, entities.PointSourceRingsClosed, &sourceID, "+10 Rail Points: all rings closed")
	}
	h.challenge.UpdateProgress(ctx, userID, "rings_all_closed_days", decimal.NewFromInt(1))
}

// OnBoostComplete is called when a user completes a boost
func (h *Hooks) OnBoostComplete(ctx context.Context, userID uuid.UUID, boostID uuid.UUID) {
	// Rail Points: 25 points for completing a boost
	if h.points != nil {
		h.points.Earn(ctx, userID, 25, entities.PointSourceBoostComplete, &boostID, "+25 Rail Points: boost complete")
	}
	h.challenge.UpdateProgress(ctx, userID, "boosts_completed_monthly", decimal.NewFromInt(1))
}
