package weekly_digest

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SpendingService provides spending data for the digest.
type SpendingService interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// BalanceProvider returns user balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// UserRepo fetches active users.
type UserRepo interface {
	GetAllActiveUsers(ctx context.Context) ([]ActiveUser, error)
}

// ActiveUser is a minimal user record for the digest.
type ActiveUser struct {
	ID uuid.UUID `db:"id"`
}

// PushSender sends push notifications.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// Worker sends weekly money digests every Sunday.
type Worker struct {
	spending SpendingService
	balances BalanceProvider
	users    UserRepo
	push     PushSender
	logger   *zap.Logger
	lastSent string // "YYYY-MM-DD"
}

func NewWorker(spending SpendingService, balances BalanceProvider, users UserRepo, push PushSender, logger *zap.Logger) *Worker {
	return &Worker{spending: spending, balances: balances, users: users, push: push, logger: logger}
}

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	now := time.Now().UTC()
	// Send on Sundays between 9-10 AM UTC
	if now.Weekday() != time.Sunday || now.Hour() != 9 {
		return
	}
	today := now.Format("2006-01-02")
	if w.lastSent == today {
		return
	}
	w.lastSent = today

	w.logger.Info("Starting weekly money digest")

	users, err := w.users.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("failed to get users for digest", zap.Error(err))
		return
	}

	weekStart := now.AddDate(0, 0, -7)
	sent := 0
	for _, u := range users {
		if err := w.sendDigest(ctx, u.ID, weekStart, now); err != nil {
			w.logger.Warn("digest failed for user", zap.String("user_id", u.ID.String()), zap.Error(err))
			continue
		}
		sent++
	}
	w.logger.Info("Weekly digest complete", zap.Int("sent", sent), zap.Int("total_users", len(users)))
}

func (w *Worker) sendDigest(ctx context.Context, userID uuid.UUID, start, end time.Time) error {
	flow, err := w.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil {
		return err
	}

	spend, _ := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)

	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)

	// Build digest message
	title := "Miriam's weekly recap"
	body := fmt.Sprintf("This week: $%s in, $%s out. ", flow.TotalDeposits.StringFixed(2), totalOut.StringFixed(2))

	if totalOut.IsZero() && flow.TotalDeposits.IsZero() {
		body = "Quiet week. No deposits, no spending, no mystery plot. "
	}

	body += fmt.Sprintf("Spend $%s. Stash $%s.", spend.StringFixed(2), stash.StringFixed(2))

	if !stash.IsZero() {
		body += " Miriam likes the Stash discipline."
	}

	return w.push.SendToUser(ctx, userID, title, body, map[string]interface{}{
		"type":      "weekly_digest",
		"deposits":  flow.TotalDeposits.String(),
		"outflows":  totalOut.String(),
		"spend_bal": spend.String(),
		"stash_bal": stash.String(),
	})
}
