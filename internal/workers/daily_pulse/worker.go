package daily_pulse

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserRepo fetches active users.
type UserRepo interface {
	GetAllActiveUsers(ctx context.Context) ([]struct {
		ID      uuid.UUID
		Country string
	}, error)
}

// BalanceProvider reads account balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// SpendingProvider reads spending data.
type SpendingProvider interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// BudgetProvider reads budget data.
type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

// StreakProvider reads streak data.
type StreakProvider interface {
	GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// PushSender sends push notifications.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// Worker sends a daily personalized money pulse notification from Miriam.
type Worker struct {
	users    UserRepo
	balances BalanceProvider
	spending SpendingProvider
	budgets  BudgetProvider
	streaks  StreakProvider
	push     PushSender
	logger   *zap.Logger
	lastDate string
}

func NewWorker(
	users UserRepo,
	balances BalanceProvider,
	spending SpendingProvider,
	budgets BudgetProvider,
	streaks StreakProvider,
	push PushSender,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		users: users, balances: balances, spending: spending,
		budgets: budgets, streaks: streaks, push: push, logger: logger,
	}
}

// Start runs the daily pulse loop. Sends at 9:00 UTC every day.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Daily pulse worker started")
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Daily pulse worker stopped")
			return
		case <-ticker.C:
			now := time.Now().UTC()
			today := now.Format("2006-01-02")
			if now.Hour() == 9 && w.lastDate != today {
				w.lastDate = today
				w.sendPulses(ctx)
			}
		}
	}
}

func (w *Worker) sendPulses(ctx context.Context) {
	users, err := w.users.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("daily pulse: failed to get users", zap.Error(err))
		return
	}

	w.logger.Info("daily pulse: sending", zap.Int("users", len(users)))
	sent, failed := 0, 0

	for _, u := range users {
		title, body := w.buildPulse(ctx, u.ID)
		if body == "" {
			continue
		}
		data := map[string]interface{}{"type": "daily_pulse", "screen": "ai-chat"}
		if err := w.push.SendToUser(ctx, u.ID, title, body, data); err != nil {
			failed++
			continue
		}
		sent++
		// Small delay to avoid hammering SNS
		time.Sleep(50 * time.Millisecond)
	}

	w.logger.Info("daily pulse: done", zap.Int("sent", sent), zap.Int("failed", failed))
}

func (w *Worker) buildPulse(ctx context.Context, userID uuid.UUID) (string, string) {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Gather data (all best-effort)
	spend, _ := w.balances.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := w.balances.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
	total := spend.Add(stash)

	var daySpend, monthNet decimal.Decimal
	if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, yesterday, now); err == nil && flow != nil {
		daySpend = flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
	}
	if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, monthStart, now); err == nil && flow != nil {
		totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
		monthNet = flow.TotalDeposits.Sub(totalOut)
	}

	var budgetRemaining decimal.Decimal
	var hasBudget bool
	if w.budgets != nil {
		if b, err := w.budgets.GetByUserID(fetchCtx, userID); err == nil && b != nil && b.MonthlyLimit.IsPositive() {
			hasBudget = true
			if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, monthStart, now); err == nil && flow != nil {
				totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
				budgetRemaining = b.MonthlyLimit.Sub(totalOut)
			}
		}
	}

	var streakDays int
	if w.streaks != nil {
		if s, err := w.streaks.GetStreak(fetchCtx, userID); err == nil && s != nil {
			streakDays = s.CurrentStreak
		}
	}

	// Pick the best pulse based on available data
	type pulse struct {
		title string
		body  string
		score int // higher = more relevant
	}
	var candidates []pulse

	// Yesterday's spending
	if daySpend.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("You spent $%s yesterday. Your total balance is $%s.", daySpend.StringFixed(2), total.StringFixed(2)),
			score: 3,
		})
	}

	// Budget progress
	if hasBudget && budgetRemaining.IsPositive() {
		daysLeft := daysRemainingInMonth(now)
		daily := budgetRemaining.Div(decimal.NewFromInt(maxInt64(int64(daysLeft), 1)))
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("$%s left in your budget this month. That's about $%s/day — you got this.", budgetRemaining.StringFixed(2), daily.StringFixed(2)),
			score: 4,
		})
	}
	if hasBudget && budgetRemaining.IsNegative() {
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("Heads up — you're $%s over budget this month. Time to chill on spending.", budgetRemaining.Abs().StringFixed(2)),
			score: 5,
		})
	}

	// Streak
	if streakDays >= 3 {
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("Day %d of your saving streak. Don't break it now.", streakDays),
			score: 2,
		})
	}

	// Stash growth
	if stash.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("Your stash is at $%s and earning yield while you sleep. Keep stacking.", stash.StringFixed(2)),
			score: 1,
		})
	}

	// Net flow
	if monthNet.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam's Daily Pulse",
			body:  fmt.Sprintf("You're up $%s this month. More in than out — that's the goal.", monthNet.StringFixed(2)),
			score: 3,
		})
	}

	// Fallback
	if len(candidates) == 0 {
		if total.IsPositive() {
			return "Miriam's Daily Pulse", fmt.Sprintf("Your balance is $%s. Your money's working even when you're not.", total.StringFixed(2))
		}
		return "", "" // No data, skip this user
	}

	// Pick highest score, with random tiebreak for variety
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	return best.title, best.body
}

func daysRemainingInMonth(now time.Time) int {
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return int(nextMonth.Sub(now).Hours() / 24)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
