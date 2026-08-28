package finance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/shopspring/decimal"
)

type SpendingAnalyzer interface {
	GetSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (*spending.Summary, error)
	GetTransactions(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error)
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
	GetDailyTrend(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByPeriod, error)
}

type AggregateStatsProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
	Upsert(ctx context.Context, userID uuid.UUID, limit decimal.Decimal, currency string) error
}

type FinancialProfileProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
	Upsert(ctx context.Context, userID uuid.UUID, update entities.FinancialProfileUpdate) (*entities.FinancialProfile, error)
}
