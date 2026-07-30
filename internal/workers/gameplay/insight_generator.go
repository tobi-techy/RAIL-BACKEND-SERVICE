package gameplay

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BalanceProvider provides user balances for insight generation
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// XPProvider provides XP data
type XPProvider interface {
	GetUserXP(ctx context.Context, userID uuid.UUID) (*entities.UserXP, error)
}

// SubscriptionChecker checks pro status for insight frequency gating
type SubscriptionChecker interface {
	IsProUser(ctx context.Context, userID uuid.UUID) (bool, error)
}

// InsightGenerator generates and sends personalized financial insights
type InsightGenerator struct {
	userProvider ActiveUserProvider
	balances     BalanceProvider
	xpProvider   XPProvider
	streakSvc    StreakService
	subChecker   SubscriptionChecker
	notifier     PushNotifier
	logger       *zap.Logger
}

func NewInsightGenerator(
	userProvider ActiveUserProvider,
	balances BalanceProvider,
	xpProvider XPProvider,
	streakSvc StreakService,
	subChecker SubscriptionChecker,
	notifier PushNotifier,
	logger *zap.Logger,
) *InsightGenerator {
	return &InsightGenerator{
		userProvider: userProvider,
		balances:     balances,
		xpProvider:   xpProvider,
		streakSvc:    streakSvc,
		subChecker:   subChecker,
		notifier:     notifier,
		logger:       logger,
	}
}

func (w *InsightGenerator) GenerateInsights(ctx context.Context) {
	userIDs, err := w.userProvider.GetActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("Failed to get users for insights", zap.Error(err))
		return
	}

	sent := 0
	for _, uid := range userIDs {
		insight := w.generateInsight(ctx, uid)
		if insight == "" {
			continue
		}
		if w.notifier != nil {
			w.notifier.SendToUser(ctx, uid, "Financial Insight", insight,
				map[string]interface{}{"type": "financial_insight"})
			sent++
		}
	}
	if sent > 0 {
		w.logger.Info("Sent financial insights", zap.Int("count", sent))
	}
}

func (w *InsightGenerator) generateInsight(ctx context.Context, userID uuid.UUID) string {
	// Try streak-based insight
	streaks, err := w.streakSvc.GetNearBreakingStreaks(ctx)
	if err == nil {
		for _, s := range streaks {
			if s.UserID == userID && s.CurrentCount > 3 {
				return fmt.Sprintf("You're on a %d-day %s streak! Keep it going — consistency builds wealth.", s.CurrentCount, s.StreakType)
			}
		}
	}

	// Try balance-based insight
	stash, err := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err == nil && stash.GreaterThan(decimal.Zero) {
		monthlyYield := stash.Mul(decimal.NewFromFloat(0.035)).Div(decimal.NewFromInt(12))
		if monthlyYield.GreaterThan(decimal.NewFromFloat(0.01)) {
			return fmt.Sprintf("Your stash is earning ~$%s/month in yield. That's money working while you sleep.", monthlyYield.StringFixed(2))
		}
	}

	// Try XP-based insight
	xp, err := w.xpProvider.GetUserXP(ctx, userID)
	if err == nil && xp != nil {
		level, _ := entities.LevelForXP(xp.TotalXP)
		if level < 10 {
			nextXP := nextLevelXP(xp.TotalXP)
			remaining := nextXP - xp.TotalXP
			return fmt.Sprintf("You're %d XP away from becoming a %s. Every deposit gets you closer!", remaining, nextLevelTitle(xp.TotalXP))
		}
	}

	return ""
}

func nextLevelXP(totalXP int64) int64 {
	for _, t := range entities.LevelThresholds {
		if totalXP < t.XP {
			return t.XP
		}
	}
	return 0
}

func nextLevelTitle(totalXP int64) string {
	for _, t := range entities.LevelThresholds {
		if totalXP < t.XP {
			return t.Title
		}
	}
	return "Legend"
}
