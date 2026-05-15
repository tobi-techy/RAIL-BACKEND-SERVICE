package wallet_migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Result holds the outcome of a migration batch.
type Result struct {
	Total      int            `json:"total"`
	Migrated   int            `json:"migrated"`
	Skipped    int            `json:"skipped"`
	Failed     int            `json:"failed"`
	Transferred decimal.Decimal `json:"transferred"`
	Errors     []ItemError    `json:"errors,omitempty"`
	Duration   time.Duration  `json:"duration"`
}

// ItemError records a single user migration failure.
type ItemError struct {
	UserID uuid.UUID `json:"user_id"`
	Chain  string    `json:"chain"`
	Reason string    `json:"reason"`
}

// Worker migrates legacy Bridge wallets to Circle and transfers residual balances.
type Worker struct {
	db            *sql.DB
	bridgeClient  *bridge.Client
	circleAdapter *circleadapter.Adapter
	walletSetID   string
	logger        *zap.Logger
}

// NewWorker creates a wallet migration worker.
func NewWorker(db *sql.DB, bridgeClient *bridge.Client, circleAdapter *circleadapter.Adapter, walletSetID string, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		bridgeClient:  bridgeClient,
		circleAdapter: circleAdapter,
		walletSetID:   walletSetID,
		logger:        logger,
	}
}

// legacyWallet is a row from managed_wallets that needs migration.
type legacyWallet struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Chain          entities.WalletChain
	BridgeWalletID string
	Address        string
}

// Run executes a migration batch. Returns results without stopping on individual failures.
func (w *Worker) Run(ctx context.Context, batchSize int) (*Result, error) {
	start := time.Now()
	res := &Result{}

	// Pass 1: Create Circle wallets for legacy Bridge-only users
	wallets, err := w.getLegacyWallets(ctx, batchSize)
	if err != nil {
		return nil, fmt.Errorf("query legacy wallets: %w", err)
	}

	for _, lw := range wallets {
		transferred, err := w.migrateOne(ctx, lw)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, ItemError{UserID: lw.UserID, Chain: string(lw.Chain), Reason: err.Error()})
			w.logger.Error("wallet migration failed", zap.String("user_id", lw.UserID.String()), zap.String("chain", string(lw.Chain)), zap.Error(err))
			continue
		}
		res.Migrated++
		res.Transferred = res.Transferred.Add(transferred)
	}

	// Pass 2: Sweep remaining Bridge balances for already-migrated wallets
	sweepWallets, err := w.getWalletsNeedingSweep(ctx, batchSize)
	if err != nil {
		w.logger.Warn("failed to query sweep wallets", zap.Error(err))
	} else {
		for _, sw := range sweepWallets {
			transferred, err := w.sweepBridgeBalance(ctx, sw)
			if err != nil {
				w.logger.Warn("bridge balance sweep failed", zap.String("user_id", sw.UserID.String()), zap.Error(err))
				continue
			}
			res.Transferred = res.Transferred.Add(transferred)
		}
	}

	res.Total = len(wallets) + len(sweepWallets)
	res.Duration = time.Since(start)
	w.logger.Info("wallet migration batch complete",
		zap.Int("total", res.Total), zap.Int("migrated", res.Migrated),
		zap.Int("failed", res.Failed), zap.String("transferred", res.Transferred.String()))
	return res, nil
}

// getLegacyWallets returns wallets that have a bridge_wallet_id but no circle_wallet_id.
func (w *Worker) getLegacyWallets(ctx context.Context, limit int) ([]legacyWallet, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, chain, bridge_wallet_id, address
		FROM managed_wallets
		WHERE bridge_wallet_id IS NOT NULL AND bridge_wallet_id != ''
		  AND (circle_wallet_id IS NULL OR circle_wallet_id = '')
		  AND status = 'live'
		ORDER BY created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []legacyWallet
	for rows.Next() {
		var lw legacyWallet
		if err := rows.Scan(&lw.ID, &lw.UserID, &lw.Chain, &lw.BridgeWalletID, &lw.Address); err != nil {
			return nil, err
		}
		out = append(out, lw)
	}
	return out, rows.Err()
}

// sweepWallet is a wallet that has both bridge and circle IDs — needs balance sweep.
type sweepWallet struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Chain          entities.WalletChain
	BridgeWalletID string
	CircleAddress  string
}

// getWalletsNeedingSweep returns wallets that have both bridge_wallet_id and circle_wallet_id set.
func (w *Worker) getWalletsNeedingSweep(ctx context.Context, limit int) ([]sweepWallet, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, chain, bridge_wallet_id, address
		FROM managed_wallets
		WHERE bridge_wallet_id IS NOT NULL AND bridge_wallet_id != ''
		  AND circle_wallet_id IS NOT NULL AND circle_wallet_id != ''
		  AND status = 'live'
		ORDER BY created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sweepWallet
	for rows.Next() {
		var sw sweepWallet
		if err := rows.Scan(&sw.ID, &sw.UserID, &sw.Chain, &sw.BridgeWalletID, &sw.CircleAddress); err != nil {
			return nil, err
		}
		out = append(out, sw)
	}
	return out, rows.Err()
}

// sweepBridgeBalance transfers any remaining USDC from a Bridge wallet to the Circle wallet address.
func (w *Worker) sweepBridgeBalance(ctx context.Context, sw sweepWallet) (decimal.Decimal, error) {
	bridgeCustomerID, err := w.getBridgeCustomerID(ctx, sw.UserID)
	if err != nil {
		return decimal.Zero, err
	}

	balance, err := w.bridgeClient.GetWalletBalance(ctx, bridgeCustomerID, sw.BridgeWalletID)
	if err != nil {
		return decimal.Zero, err
	}

	var transferred decimal.Decimal
	for _, entry := range balance.Balances {
		amt, err := decimal.NewFromString(entry.Balance)
		if err != nil {
			w.logger.Error("failed to parse balance during sweep",
				zap.String("user_id", sw.UserID.String()), zap.String("balance", entry.Balance), zap.Error(err))
			continue
		}
		if amt.LessThanOrEqual(decimal.Zero) {
			continue
		}
		if !strings.EqualFold(string(entry.Currency), "usdc") {
			continue
		}

		_, err = w.bridgeClient.CreateTransfer(ctx, &bridge.CreateTransferRequest{
			OnBehalfOf: bridgeCustomerID,
			Amount:     amt.String(),
			Source: bridge.TransferSource{
				PaymentRail:    bridge.PaymentRail("bridge_wallet"),
				Currency:       bridge.CurrencyUSDC,
				BridgeWalletID: sw.BridgeWalletID,
			},
			Destination: bridge.TransferDestination{
				PaymentRail: chainToPaymentRail(sw.Chain),
				Currency:    bridge.CurrencyUSDC,
				ToAddress:   sw.CircleAddress,
			},
		})
		if err != nil {
			w.logger.Error("bridge sweep transfer failed",
				zap.String("user_id", sw.UserID.String()), zap.String("amount", amt.String()), zap.Error(err))
			return decimal.Zero, fmt.Errorf("sweep transfer of %s USDC failed: %w", amt.String(), err)
		}
		transferred = transferred.Add(amt)
		w.logger.Info("bridge balance swept to circle",
			zap.String("user_id", sw.UserID.String()), zap.String("amount", amt.String()), zap.String("chain", string(sw.Chain)))
	}
	return transferred, nil
}

// migrateOne creates a Circle wallet for the user on the same chain, transfers any Bridge balance, and updates the DB row.
func (w *Worker) migrateOne(ctx context.Context, lw legacyWallet) (decimal.Decimal, error) {
	// 1. Create Circle wallet
	circleWallet, err := w.circleAdapter.CreateWalletForUser(ctx, lw.UserID, w.walletSetID, lw.Chain)
	if err != nil {
		return decimal.Zero, fmt.Errorf("create circle wallet: %w", err)
	}

	// 2. Check Bridge wallet balance
	bridgeCustomerID, err := w.getBridgeCustomerID(ctx, lw.UserID)
	if err != nil {
		w.logger.Warn("cannot check bridge balance — will retry on next run",
			zap.String("user_id", lw.UserID.String()), zap.Error(err))
		return decimal.Zero, fmt.Errorf("get bridge customer id: %w", err)
	}

	balance, err := w.bridgeClient.GetWalletBalance(ctx, bridgeCustomerID, lw.BridgeWalletID)
	if err != nil {
		w.logger.Error("failed to check bridge balance, migration incomplete",
			zap.String("user_id", lw.UserID.String()), zap.Error(err))
		return decimal.Zero, fmt.Errorf("get wallet balance: %w", err)
	}

	// 3. Transfer any USDC balance to the new Circle wallet
	var transferred decimal.Decimal
	for _, entry := range balance.Balances {
		amt, err := decimal.NewFromString(entry.Balance)
		if err != nil {
			w.logger.Error("failed to parse balance during migration",
				zap.String("user_id", lw.UserID.String()), zap.String("balance", entry.Balance), zap.Error(err))
			continue
		}
		if amt.LessThanOrEqual(decimal.Zero) {
			continue
		}
		if !strings.EqualFold(string(entry.Currency), "usdc") {
			continue
		}

		_, err = w.bridgeClient.CreateTransfer(ctx, &bridge.CreateTransferRequest{
			OnBehalfOf: bridgeCustomerID,
			Amount:     amt.String(),
			Source: bridge.TransferSource{
				PaymentRail:    bridge.PaymentRail("bridge_wallet"),
				Currency:       bridge.CurrencyUSDC,
				BridgeWalletID: lw.BridgeWalletID,
			},
			Destination: bridge.TransferDestination{
				PaymentRail: chainToPaymentRail(lw.Chain),
				Currency:    bridge.CurrencyUSDC,
				ToAddress:   circleWallet.Address,
			},
		})
		if err != nil {
			w.logger.Error("bridge transfer failed during migration",
				zap.String("user_id", lw.UserID.String()), zap.String("amount", amt.String()), zap.Error(err))
			return decimal.Zero, fmt.Errorf("bridge transfer of %s USDC failed: %w", amt.String(), err)
		}
		transferred = transferred.Add(amt)
	}

	// 4. Update the managed_wallets row
	w.updateCircleWalletID(ctx, lw.ID, circleWallet.CircleWalletID, circleWallet.Address)

	return transferred, nil
}

func (w *Worker) updateCircleWalletID(ctx context.Context, walletID uuid.UUID, circleWalletID, newAddress string) {
	_, err := w.db.ExecContext(ctx, `
		UPDATE managed_wallets
		SET circle_wallet_id = $1, address = $2, updated_at = NOW()
		WHERE id = $3`, circleWalletID, newAddress, walletID)
	if err != nil {
		w.logger.Error("failed to update circle_wallet_id", zap.String("wallet_id", walletID.String()), zap.Error(err))
	}
}

func (w *Worker) getBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error) {
	var customerID string
	err := w.db.QueryRowContext(ctx, `SELECT bridge_customer_id FROM users WHERE id = $1`, userID).Scan(&customerID)
	if err != nil {
		return "", err
	}
	if customerID == "" {
		return "", fmt.Errorf("user has no bridge_customer_id")
	}
	return customerID, nil
}

func chainToPaymentRail(chain entities.WalletChain) bridge.PaymentRail {
	switch chain {
	case entities.WalletChainSolana:
		return bridge.PaymentRailSolana
	case entities.WalletChainEthereum:
		return bridge.PaymentRailEthereum
	case entities.WalletChainPolygon:
		return bridge.PaymentRailPolygon
	case entities.WalletChainBase:
		return bridge.PaymentRailBase
	case entities.WalletChainArbitrum:
		return bridge.PaymentRailArbitrum
	case entities.WalletChainOptimism:
		return bridge.PaymentRailOptimism
	case entities.WalletChainAvalanche:
		return bridge.PaymentRailAvalanche
	default:
		return bridge.PaymentRailSolana
	}
}
