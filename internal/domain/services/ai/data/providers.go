package data

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type CardTransactionProvider interface {
	GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error)
}

type DepositHistoryProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	GetByUserIDInRange(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.Deposit, error)
}

type DepositIncomeProvider interface {
	GetCompletedMonthlyTotals(ctx context.Context, userID uuid.UUID, since, until time.Time) ([]entities.DepositMonthlyTotal, error)
}

type YieldProvider interface {
	GetSnapshotsInWindow(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]*entities.YieldBalanceSnapshot, error)
}

type WithdrawalHistoryProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	GetByUserIDInRange(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.Withdrawal, error)
}

type ReceiptHistoryProvider interface {
	GetByUserIDInRange(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.ReceiptScan, error)
	GetTotalByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error)
}
