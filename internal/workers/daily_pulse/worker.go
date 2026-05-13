package daily_pulse

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
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

// NudgeGenerator generates AI-powered nudge text. Nil-safe — returns "" if not set.
type NudgeGenerator interface {
	GenerateNudge(ctx context.Context, snapshot string) string
}

// Worker sends a daily personalized money pulse notification from Miriam.
type Worker struct {
	users    UserRepo
	balances BalanceProvider
	spending SpendingProvider
	budgets  BudgetProvider
	streaks  StreakProvider
	push     PushSender
	nudger   NudgeGenerator
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

// SetNudger sets an optional AI nudge generator for personalized pulse messages.
func (w *Worker) SetNudger(n NudgeGenerator) { w.nudger = n }

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

	// Try AI-generated nudge first (fast model, ~1s)
	if w.nudger != nil {
		snapshot := buildNudgeSnapshot(total, spend, stash, daySpend, monthNet, budgetRemaining, hasBudget, streakDays, now)
		if nudge := w.nudger.GenerateNudge(fetchCtx, snapshot); nudge != "" {
			return "Miriam checked the math", nudge
		}
	}

	// Fallback to template-based pulse
	type pulse struct {
		title string
		body  string
		score int // higher = more relevant
	}
	var candidates []pulse

	// Yesterday's spending
	if daySpend.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam spotted a move",
			body:  fmt.Sprintf("$%s left Spend yesterday. Want the quick read on what changed?", daySpend.StringFixed(2)),
			score: 3,
		})
	}

	// Budget progress
	if hasBudget && budgetRemaining.IsPositive() {
		daysLeft := daysRemainingInMonth(now)
		daily := budgetRemaining.Div(decimal.NewFromInt(maxInt64(int64(daysLeft), 1)))
		candidates = append(candidates, pulse{
			title: "Miriam likes this pace",
			body:  fmt.Sprintf("$%s left this month. That's about $%s/day if we keep it tidy.", budgetRemaining.StringFixed(2), daily.StringFixed(2)),
			score: 4,
		})
	}
	if hasBudget && budgetRemaining.IsNegative() {
		candidates = append(candidates, pulse{
			title: "Tiny budget reset?",
			body:  fmt.Sprintf("Budget is $%s over. Not a crisis, just a course correction.", budgetRemaining.Abs().StringFixed(2)),
			score: 5,
		})
	}

	// Streak
	if streakDays >= 3 {
		candidates = append(candidates, pulse{
			title: "Streak is still alive",
			body:  fmt.Sprintf("Day %d. Your future self is quietly enjoying this consistency.", streakDays),
			score: 2,
		})
	}

	// Stash growth
	if stash.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Stash check",
			body:  fmt.Sprintf("$%s in Stash. Quiet money, doing useful things.", stash.StringFixed(2)),
			score: 1,
		})
	}

	// Net flow
	if monthNet.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam did a small nod",
			body:  fmt.Sprintf("You're up $%s this month. More in than out is the whole trick.", monthNet.StringFixed(2)),
			score: 3,
		})
	}

	// Fallback
	if len(candidates) == 0 {
		if total.IsPositive() {
			return "Miriam checked in", fmt.Sprintf("$%s across Rail. Your money clocked in before you did.", total.StringFixed(2))
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

func buildNudgeSnapshot(total, spend, stash, daySpend, monthNet, budgetRemaining decimal.Decimal, hasBudget bool, streakDays int, now time.Time) string {
	var parts []string
	if total.IsPositive() {
		parts = append(parts, fmt.Sprintf("Total: $%s (spend $%s, stash $%s)", total.StringFixed(2), spend.StringFixed(2), stash.StringFixed(2)))
	}
	if daySpend.IsPositive() {
		parts = append(parts, fmt.Sprintf("Yesterday spent: $%s", daySpend.StringFixed(2)))
	}
	if !monthNet.IsZero() {
		parts = append(parts, fmt.Sprintf("Month net: $%s", monthNet.StringFixed(2)))
	}
	if hasBudget {
		parts = append(parts, fmt.Sprintf("Budget remaining: $%s", budgetRemaining.StringFixed(2)))
	}
	if streakDays > 0 {
		parts = append(parts, fmt.Sprintf("Saving streak: %d days", streakDays))
	}
	parts = append(parts, fmt.Sprintf("Day: %s", now.Format("Monday")))
	return fmt.Sprintf("%s", strings.Join(parts, ". "))
}
