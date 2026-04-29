package memory_worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SpendingProvider reads spending data for pattern detection.
type SpendingProvider interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// BalanceProvider reads balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// Worker runs periodic memory maintenance: transaction pattern detection, decay, and summarization.
type Worker struct {
	memory   *aiservice.MemoryService
	spending SpendingProvider
	balances BalanceProvider
	logger   *zap.Logger
}

func NewWorker(memory *aiservice.MemoryService, spending SpendingProvider, balances BalanceProvider, logger *zap.Logger) *Worker {
	return &Worker{memory: memory, spending: spending, balances: balances, logger: logger}
}

// Start runs the memory worker loop. Runs daily at 3am UTC.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("memory worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	var lastRun string
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory worker stopped")
			return
		case <-ticker.C:
			now := time.Now().UTC()
			today := now.Format("2006-01-02")
			if now.Hour() == 3 && lastRun != today {
				lastRun = today
				w.runAll(ctx)
			}
		}
	}
}

func (w *Worker) runAll(ctx context.Context) {
	w.logger.Info("memory worker: starting daily run")

	// 1. Decay stale facts
	if err := w.memory.RunDecay(ctx); err != nil {
		w.logger.Error("memory worker: decay failed", zap.Error(err))
	}

	// 2. Detect transaction patterns for all users with memory
	w.detectPatterns(ctx)

	// 3. Summarize memory for users with many facts
	w.summarizeMemories(ctx)

	w.logger.Info("memory worker: daily run complete")
}

func (w *Worker) detectPatterns(ctx context.Context) {
	userIDs, err := w.memory.ListActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("memory worker: failed to get user IDs", zap.Error(err))
		return
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	prevMonthStart := monthStart.AddDate(0, -1, 0)
	weekAgo := now.AddDate(0, 0, -7)

	detected := 0
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return
		}
		n := w.detectUserPatterns(ctx, userID, monthStart, prevMonthStart, weekAgo, now)
		detected += n
		time.Sleep(100 * time.Millisecond) // Rate limit
	}
	w.logger.Info("memory worker: patterns detected", zap.Int("total", detected), zap.Int("users", len(userIDs)))
}

func (w *Worker) detectUserPatterns(ctx context.Context, userID uuid.UUID, monthStart, prevMonthStart, weekAgo, now time.Time) int {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	detected := 0

	// Current month flow
	flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, monthStart, now)
	if err != nil || flow == nil {
		return 0
	}
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)

	// Previous month flow for comparison
	prevFlow, _ := w.spending.GetMoneyFlow(fetchCtx, userID, prevMonthStart, monthStart)

	// Week flow
	weekFlow, _ := w.spending.GetMoneyFlow(fetchCtx, userID, weekAgo, now)

	// Pattern: spending spike (this month > 1.5x last month)
	if prevFlow != nil {
		prevOut := prevFlow.TotalWithdrawals.Add(prevFlow.TotalCardSpend).Add(prevFlow.TotalP2P)
		if prevOut.IsPositive() && totalOut.GreaterThan(prevOut.Mul(decimal.NewFromFloat(1.5))) {
			pattern := "Spending significantly higher than last month"
			if err := w.memory.SaveTransactionPattern(fetchCtx, userID, pattern, entities.FactCategoryFinancialBehavior, 0.7); err == nil {
				detected++
			}
		}
	}

	// Pattern: consistent saver (deposits > spending for 2+ months)
	if flow.TotalDeposits.GreaterThan(totalOut) && prevFlow != nil {
		prevOut := prevFlow.TotalWithdrawals.Add(prevFlow.TotalCardSpend).Add(prevFlow.TotalP2P)
		if prevFlow.TotalDeposits.GreaterThan(prevOut) {
			if err := w.memory.SaveTransactionPattern(fetchCtx, userID, "Consistently saves more than they spend", entities.FactCategoryFinancialBehavior, 0.8); err == nil {
				detected++
			}
		}
	}

	// Pattern: stash discipline (low withdrawals relative to stash balance)
	stash, _ := w.balances.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
	if stash.GreaterThan(decimal.NewFromInt(100)) {
		if flow.TotalWithdrawals.IsZero() || flow.TotalWithdrawals.LessThan(stash.Mul(decimal.NewFromFloat(0.1))) {
			if err := w.memory.SaveTransactionPattern(fetchCtx, userID, "Keeps a healthy stash balance with minimal withdrawals", entities.FactCategoryFinancialBehavior, 0.7); err == nil {
				detected++
			}
		}
	}

	// Pattern: weekend spender (week flow significantly higher than weekday average)
	if weekFlow != nil {
		weekOut := weekFlow.TotalWithdrawals.Add(weekFlow.TotalCardSpend).Add(weekFlow.TotalP2P)
		if weekOut.IsPositive() && totalOut.IsPositive() {
			weeklyRate := weekOut.Div(decimal.NewFromInt(7))
			monthlyRate := totalOut.Div(decimal.NewFromInt(int64(now.Day())))
			if weeklyRate.GreaterThan(monthlyRate.Mul(decimal.NewFromFloat(1.3))) {
				if err := w.memory.SaveTransactionPattern(fetchCtx, userID, "Spending has been higher than usual this week", entities.FactCategoryHabit, 0.6); err == nil {
					detected++
				}
			}
		}
	}

	return detected
}

func (w *Worker) summarizeMemories(ctx context.Context) {
	userIDs, err := w.memory.ListActiveUserIDs(ctx)
	if err != nil {
		return
	}

	summarized := 0
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return
		}
		sumCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := w.memory.SummarizeMemory(sumCtx, userID); err != nil {
			w.logger.Debug("memory summarization skipped", zap.String("user_id", userID.String()), zap.Error(err))
		} else {
			summarized++
		}
		cancel()
		time.Sleep(200 * time.Millisecond)
	}
	if summarized > 0 {
		w.logger.Info("memory worker: summaries updated", zap.Int("count", summarized))
	}
}
