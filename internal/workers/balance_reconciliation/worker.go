package balance_reconciliation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// CircleClient fetches wallet balances from Circle
type CircleClient interface {
	GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error)
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
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error)
}

// Worker reconciles ledger balances with Circle wallet balances
type Worker struct {
	circleClient   CircleClient
	ledgerService  LedgerService
	walletRepo     WalletRepository
	ledgerRepo     LedgerRepository
	checkInterval  time.Duration
	threshold      decimal.Decimal // Only reconcile if diff exceeds this
	logger         *zap.Logger
	stopCh         chan struct{}
}

// NewWorker creates a new balance reconciliation worker
func NewWorker(
	circleClient CircleClient,
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
		threshold = decimal.NewFromFloat(0.01) // 1 cent threshold
	}
	return &Worker{
		circleClient:  circleClient,
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
		if wallet.CircleWalletID == "" {
			continue
		}

		// Get Circle balance
		balances, err := w.circleClient.GetWalletBalances(ctx, wallet.CircleWalletID)
		if err != nil {
			w.logger.Warn("Failed to get Circle balance", zap.String("wallet_id", wallet.CircleWalletID), zap.Error(err))
			failed++
			continue
		}

		var circleBalance decimal.Decimal
		for _, tb := range balances.TokenBalances {
			if tb.Token.Symbol == "USDC" {
				circleBalance, _ = decimal.NewFromString(tb.Amount)
				break
			}
		}

		// Get ledger balance
		accounts, err := w.ledgerRepo.GetByUserID(ctx, wallet.UserID)
		if err != nil {
			w.logger.Warn("Failed to get ledger", zap.String("user_id", wallet.UserID.String()), zap.Error(err))
			failed++
			continue
		}

		var ledgerBalance decimal.Decimal
		for _, acc := range accounts {
			if acc.AccountType == entities.AccountTypeSpendingBalance {
				ledgerBalance = acc.Balance
				break
			}
		}

		diff := circleBalance.Sub(ledgerBalance).Abs()
		if diff.LessThanOrEqual(w.threshold) {
			skipped++
			continue
		}

		// Reconcile
		if err := w.ledgerService.ReconcileBalance(ctx, wallet.UserID, entities.AccountTypeSpendingBalance, circleBalance); err != nil {
			w.logger.Error("Failed to reconcile",
				zap.String("user_id", wallet.UserID.String()),
				zap.String("ledger", ledgerBalance.String()),
				zap.String("circle", circleBalance.String()),
				zap.Error(err))
			failed++
			continue
		}

		w.logger.Info("Reconciled balance",
			zap.String("user_id", wallet.UserID.String()),
			zap.String("old", ledgerBalance.String()),
			zap.String("new", circleBalance.String()),
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
