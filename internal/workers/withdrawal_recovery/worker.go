package withdrawal_recovery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type LedgerReverser interface {
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// Worker periodically finds crypto withdrawals stuck in processing/initiated
// status and reverses the ledger debit if the provider transfer never completed.
type Worker struct {
	db            *sql.DB
	ledger        LedgerReverser
	logger        *zap.Logger
	checkInterval time.Duration
	maxStuckAge   time.Duration
	stopCh        chan struct{}
}

func NewWorker(db *sql.DB, ledger LedgerReverser, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		ledger:        ledger,
		logger:        logger,
		checkInterval: 5 * time.Minute,
		maxStuckAge:   30 * time.Minute,
		stopCh:        make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting withdrawal recovery worker",
		zap.Duration("interval", w.checkInterval),
		zap.Duration("max_stuck_age", w.maxStuckAge))

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.recover(ctx)
		}
	}
}

func (w *Worker) Stop() { close(w.stopCh) }

func (w *Worker) recover(ctx context.Context) {
	maxAgeSeconds := int(w.maxStuckAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, amount, fee_amount, source_account, status, updated_at
		FROM withdrawals
		WHERE status IN ('initiated', 'processing')
		  AND withdrawal_type = 'crypto'
		  AND (bridge_transfer_id IS NULL OR bridge_transfer_id = '')
		  AND updated_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("withdrawal recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuck struct {
		ID            uuid.UUID
		UserID        uuid.UUID
		Amount        decimal.Decimal
		FeeAmount     decimal.Decimal
		SourceAccount string
		Status        string
		UpdatedAt     time.Time
	}

	var items []stuck
	for rows.Next() {
		var s stuck
		if err := rows.Scan(&s.ID, &s.UserID, &s.Amount, &s.FeeAmount, &s.SourceAccount, &s.Status, &s.UpdatedAt); err != nil {
			w.logger.Error("withdrawal recovery: scan failed", zap.Error(err))
			continue
		}
		items = append(items, s)
	}

	for _, s := range items {
		if err := w.recoverStuckWithdrawal(ctx, s.ID, s.UserID, s.Amount.Add(s.FeeAmount), s.SourceAccount, s.Status); err != nil {
			w.logger.Error("withdrawal recovery: reversal failed",
				zap.Error(err), zap.String("withdrawal_id", s.ID.String()))
		}
	}
}

func (w *Worker) recoverStuckWithdrawal(ctx context.Context, withdrawalID, userID uuid.UUID, totalAmount decimal.Decimal, sourceAccount, status string) error {
	if status == string(entities.WithdrawalStatusInitiated) {
		_, err := w.db.ExecContext(ctx, `
			UPDATE withdrawals
			SET status = 'failed', error_message = 'auto-failed: stuck before ledger debit', updated_at = NOW()
			WHERE id = $1 AND status = 'initiated'`, withdrawalID)
		return err
	}

	if status != string(entities.WithdrawalStatusProcessing) {
		return nil
	}

	if w.ledger == nil {
		return fmt.Errorf("ledger reverser not configured")
	}

	accountType := entities.AccountTypeSpendingBalance
	if sourceAccount == string(entities.WithdrawalSourceStashBalance) {
		accountType = entities.AccountTypeStashBalance
	}

	if err := w.ledger.ReverseTransaction(ctx, userID, accountType, withdrawalID.String(), totalAmount, map[string]interface{}{
		"withdrawal_id":   withdrawalID.String(),
		"reversal_reason": "auto_recovery_stuck_processing",
		"source_account":  sourceAccount,
	}); err != nil {
		return err
	}

	res, err := w.db.ExecContext(ctx, `
		UPDATE withdrawals
		SET status = 'reversed', error_message = 'auto-reversed: stuck in processing without provider transfer', updated_at = NOW()
		WHERE id = $1 AND status = 'processing'`, withdrawalID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil
	}

	w.logger.Info("Withdrawal auto-reversed",
		zap.String("withdrawal_id", withdrawalID.String()),
		zap.String("user_id", userID.String()),
		zap.String("amount", totalAmount.String()))
	return nil
}
