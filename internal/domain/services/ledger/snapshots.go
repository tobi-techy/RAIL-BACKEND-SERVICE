package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SnapshotWriter handles balance snapshot creation and queries.
type SnapshotWriter struct {
	repo snapshotRepository
}

// snapshotRepository is the narrow interface the snapshot writer needs.
type snapshotRepository interface {
	InsertBalanceSnapshot(ctx context.Context, accountID uuid.UUID, balance decimal.Decimal, date time.Time) error
	GetBalanceSnapshot(ctx context.Context, accountID uuid.UUID, date time.Time) (*decimal.Decimal, error)
	GetLatestSnapshotDate(ctx context.Context) (*time.Time, error)
	GetAllAccountIDs(ctx context.Context) ([]uuid.UUID, error)
	GetAccountBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error)
}

// NewSnapshotWriter creates a new snapshot writer.
func NewSnapshotWriter(repo snapshotRepository) *SnapshotWriter {
	return &SnapshotWriter{repo: repo}
}

// RecordDailySnapshots records the balance of every account for today's date.
// Idempotent: if a snapshot already exists for an account+date, it is skipped.
func (w *SnapshotWriter) RecordDailySnapshots(ctx context.Context, snapshotDate time.Time) (int, error) {
	accounts, err := w.repo.GetAllAccountIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("get all account IDs: %w", err)
	}

	date := snapshotDate.Truncate(24 * time.Hour)
	var count int

	for _, accountID := range accounts {
		balance, err := w.repo.GetAccountBalance(ctx, accountID)
		if err != nil {
			return count, fmt.Errorf("get balance for account %s: %w", accountID, err)
		}

		if err := w.repo.InsertBalanceSnapshot(ctx, accountID, balance, date); err != nil {
			// If it's a unique violation, another worker already recorded this snapshot.
			// This is expected during concurrent runs.
			return count, fmt.Errorf("insert snapshot for account %s: %w", accountID, err)
		}
		count++
	}

	return count, nil
}

// GetSnapshotBalance retrieves the balance for an account at a specific date.
func (w *SnapshotWriter) GetSnapshotBalance(ctx context.Context, accountID uuid.UUID, date time.Time) (*decimal.Decimal, error) {
	return w.repo.GetBalanceSnapshot(ctx, accountID, date)
}

// GetLatestSnapshotDate returns the most recent date for which snapshots exist.
func (w *SnapshotWriter) GetLatestSnapshotDate(ctx context.Context) (*time.Time, error) {
	return w.repo.GetLatestSnapshotDate(ctx)
}
