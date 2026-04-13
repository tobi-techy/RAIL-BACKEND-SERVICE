package ai_insights

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserRepo fetches active users.
type UserRepo interface {
	GetAllActiveUsers(ctx context.Context) ([]repositories.NotificationUser, error)
}

// PushSender sends push notifications.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// SpendingRepo provides spending data for alerts.
type SpendingRepo interface {
	GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error)
}

// BalanceProvider provides current balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// Worker runs periodic proactive insight checks and sends push notifications.
type Worker struct {
	userRepo     UserRepo
	pushSender   PushSender
	spendingRepo SpendingRepo
	balances     BalanceProvider
	logger       *zap.Logger
	lastDigestDay int // day of year when last digest ran
}

func NewWorker(userRepo UserRepo, pushSender PushSender, spendingRepo SpendingRepo, balances BalanceProvider, logger *zap.Logger) *Worker {
	return &Worker{
		userRepo:     userRepo,
		pushSender:   pushSender,
		spendingRepo: spendingRepo,
		balances:     balances,
		logger:       logger,
	}
}

// Start runs the insight check loop. Checks hourly.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("AI insights worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("AI insights worker stopped")
			return
		case t := <-ticker.C:
			hour := t.UTC().Hour()
			weekday := t.UTC().Weekday()

			// Daily insights at 9am UTC
			if hour == 9 {
				w.runDailyInsights(ctx)
			}

			// Weekly digest on Mondays at 8am UTC
			if weekday == time.Monday && hour == 8 && t.UTC().YearDay() != w.lastDigestDay {
				w.lastDigestDay = t.UTC().YearDay()
				w.runWeeklyDigest(ctx)
			}
		}
	}
}

func (w *Worker) runDailyInsights(ctx context.Context) {
	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("failed to get active users", zap.Error(err))
		return
	}

	w.logger.Info("Running daily insights", zap.Int("users", len(users)))
	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		userCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		w.checkSpendingAlert(userCtx, u.ID)
		w.checkSavingsGrowth(userCtx, u.ID)
		cancel()
	}
}

// checkSpendingAlert compares this week's spending to last week's.
func (w *Worker) checkSpendingAlert(ctx context.Context, userID uuid.UUID) {
	now := time.Now().UTC()
	thisWeekStart := now.AddDate(0, 0, -7)
	lastWeekStart := now.AddDate(0, 0, -14)

	thisWeek, _, err := w.spendingRepo.GetSpendingTotal(ctx, userID, thisWeekStart, now)
	if err != nil || thisWeek.IsZero() {
		return
	}

	lastWeek, _, err := w.spendingRepo.GetSpendingTotal(ctx, userID, lastWeekStart, thisWeekStart)
	if err != nil || lastWeek.IsZero() {
		return
	}

	ratio := thisWeek.Div(lastWeek)

	if ratio.GreaterThan(decimal.NewFromFloat(1.5)) {
		pctIncrease := ratio.Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100)).StringFixed(0)
		_ = w.pushSender.SendToUser(ctx, userID,
			"Spending Alert",
			"You've spent "+pctIncrease+"% more this week than last week. Tap to see where your money went.",
			map[string]interface{}{"type": "spending_alert", "action": "open_chat"},
		)
	}
}

// checkSavingsGrowth sends an encouraging notification when stash grows.
func (w *Worker) checkSavingsGrowth(ctx context.Context, userID uuid.UUID) {
	balance, err := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil || balance.IsZero() {
		return
	}

	// Milestone notifications at $100, $500, $1000, $5000
	milestones := []decimal.Decimal{
		decimal.NewFromInt(100),
		decimal.NewFromInt(500),
		decimal.NewFromInt(1000),
		decimal.NewFromInt(5000),
	}

	for _, m := range milestones {
		if balance.GreaterThanOrEqual(m) && balance.Sub(m).LessThan(decimal.NewFromInt(10)) {
			_ = w.pushSender.SendToUser(ctx, userID,
				"Savings Milestone!",
				"Your stash just crossed $"+m.String()+". Your money is working for you.",
				map[string]interface{}{"type": "savings_milestone", "amount": m.String()},
			)
			return
		}
	}
}

// runWeeklyDigest sends a weekly financial summary to each user.
func (w *Worker) runWeeklyDigest(ctx context.Context) {
	w.logger.Info("Running weekly digest")

	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("weekly digest: failed to get users", zap.Error(err))
		return
	}

	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -7)

	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		spent, txCount, err := w.spendingRepo.GetSpendingTotal(ctx, u.ID, weekStart, now)
		if err != nil {
			continue
		}

		stash, err := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeStashBalance)
		if err != nil {
			continue
		}

		spend, err := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
		if err != nil {
			continue
		}

		total := spend.Add(stash)
		if total.IsZero() && spent.IsZero() {
			continue // Skip inactive users
		}

		body := "This week: "
		if txCount > 0 {
			body += "$" + spent.StringFixed(2) + " spent across " + fmt.Sprintf("%d", txCount) + " transactions. "
		} else {
			body += "No spending this week. "
		}
		body += "Stash: $" + stash.StringFixed(2) + ". Total: $" + total.StringFixed(2) + "."

		_ = w.pushSender.SendToUser(ctx, u.ID,
			"Your Weekly Money Recap",
			body,
			map[string]interface{}{"type": "weekly_digest", "action": "open_chat"},
		)
	}

	w.logger.Info("Weekly digest complete", zap.Int("users", len(users)))
}
