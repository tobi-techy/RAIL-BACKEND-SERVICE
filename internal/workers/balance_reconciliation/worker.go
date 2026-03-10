package balance_reconciliation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// WalletBalanceClient fetches wallet balances from Bridge
type WalletBalanceClient interface {
	GetWalletBalance(ctx context.Context, customerID, walletID string) (string, error)
}

// LedgerService reconciles ledger balances
type LedgerService interface {
	ReconcileBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, newBalance decimal.Decimal) error
}

// WalletRepository fetches user wallets
type WalletRepository interface {
	GetAllActiveWallets(ctx context.Context) ([]*entities.ManagedWallet, error)
}

// LedgerRepository fetches ledger accounts
type LedgerRepository interface {
	GetAccountByUserAndType(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
}

// Worker reconciles ledger balances with Bridge wallet balances
type Worker struct {
	walletClient  WalletBalanceClient
	ledgerService LedgerService
	walletRepo    WalletRepository
	ledgerRepo    LedgerRepository
	checkInterval time.Duration
	threshold     decimal.Decimal
	logger        *zap.Logger
	stopCh        chan struct{}
}

// NewWorker creates a new balance reconciliation worker
func NewWorker(
	walletClient WalletBalanceClient,
	ledgerService LedgerService,
	walletRepo WalletRepository,
	ledgerRepo LedgerRepository,
	checkInterval time.Duration,
	threshold decimal.Decimal,
	logger *zap.Logger,
) *Worker {
	if checkInterval == 0 {
		checkInterval = 6 * time.Hour
	}
	if threshold.IsZero() {
		threshold = decimal.NewFromFloat(0.01)
	}
	return &Worker{
		walletClient:  walletClient,
		ledgerService: ledgerService,
		walletRepo:    walletRepo,
		ledgerRepo:    ledgerRepo,
		checkInterval: checkInterval,
		threshold:     threshold,
		logger:        logger,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the reconciliation worker
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting balance reconciliation worker", zap.Duration("interval", w.checkInterval))

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Balance reconciliation worker stopped")
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

// Stop stops the worker
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) reconcile(ctx context.Context) {
	wallets, err := w.walletRepo.GetAllActiveWallets(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch wallets", zap.Error(err))
		return
	}

	var reconciled, skipped, failed int
	for _, wallet := range wallets {
		if wallet.BridgeWalletID == "" {
			continue
		}

		// Get Bridge balance — customerID is stored as BridgeCustomerID on the user profile;
		// use UserID string as a fallback key since we don't have it here.
		amountStr, err := w.walletClient.GetWalletBalance(ctx, wallet.UserID.String(), wallet.BridgeWalletID)
		if err != nil {
			w.logger.Warn("Failed to get Bridge balance", zap.String("wallet_id", wallet.BridgeWalletID), zap.Error(err))
			failed++
			continue
		}

		bridgeBalance, _ := decimal.NewFromString(amountStr)

		// Get ledger balance
		account, err := w.ledgerRepo.GetAccountByUserAndType(ctx, wallet.UserID, entities.AccountTypeSpendingBalance)
		if err != nil {
			w.logger.Warn("Failed to get ledger", zap.String("user_id", wallet.UserID.String()), zap.Error(err))
			failed++
			continue
		}

		ledgerBalance := account.Balance
		diff := bridgeBalance.Sub(ledgerBalance).Abs()
		if diff.LessThanOrEqual(w.threshold) {
			skipped++
			continue
		}

		if err := w.ledgerService.ReconcileBalance(ctx, wallet.UserID, entities.AccountTypeSpendingBalance, bridgeBalance); err != nil {
			w.logger.Error("Failed to reconcile",
				zap.String("user_id", wallet.UserID.String()),
				zap.String("ledger", ledgerBalance.String()),
				zap.String("bridge", bridgeBalance.String()),
				zap.Error(err))
			failed++
			continue
		}

		w.logger.Info("Reconciled balance",
			zap.String("user_id", wallet.UserID.String()),
			zap.String("old", ledgerBalance.String()),
			zap.String("new", bridgeBalance.String()),
			zap.String("diff", diff.String()))
		reconciled++
	}

	if reconciled > 0 || failed > 0 {
		w.logger.Info("Reconciliation complete",
			zap.Int("reconciled", reconciled),
			zap.Int("skipped", skipped),
			zap.Int("failed", failed))
	}
}
