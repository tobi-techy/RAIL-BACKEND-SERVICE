package deposit_allocation_recovery

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

// AllocationService defines the allocation processing dependency.
type AllocationService interface {
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// Config controls deposit allocation recovery worker behavior.
type Config struct {
	CheckInterval time.Duration
	BatchSize     int
	MaxDepositAge time.Duration
}

// DefaultConfig returns sensible defaults for periodic recovery.
func DefaultConfig() *Config {
	return &Config{
		CheckInterval: 15 * time.Second,
		BatchSize:     100,
		MaxDepositAge: 30 * time.Minute,
	}
}

type depositCandidate struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Amount    decimal.Decimal
	TxHash    string
	Chain     string
	Token     string
	CreatedAt time.Time
}

// Worker periodically retries allocation for deposits that were confirmed but never split.
type Worker struct {
	db                *sql.DB
	allocationService AllocationService
	logger            *zap.Logger
	checkInterval     time.Duration
	batchSize         int
	maxDepositAge     time.Duration
	stopCh            chan struct{}
}

// NewWorker creates a new deposit allocation recovery worker.
func NewWorker(db *sql.DB, allocationService AllocationService, logger *zap.Logger, config *Config) *Worker {
	if config == nil {
		config = DefaultConfig()
	}
	if config.CheckInterval <= 0 {
		config.CheckInterval = DefaultConfig().CheckInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultConfig().BatchSize
	}
	if config.MaxDepositAge <= 0 {
		config.MaxDepositAge = DefaultConfig().MaxDepositAge
	}

	return &Worker{
		db:                db,
		allocationService: allocationService,
		logger:            logger,
		checkInterval:     config.CheckInterval,
		batchSize:         config.BatchSize,
		maxDepositAge:     config.MaxDepositAge,
		stopCh:            make(chan struct{}),
	}
}

// Start begins periodic reconciliation.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting deposit allocation recovery worker",
		zap.Duration("check_interval", w.checkInterval),
		zap.Int("batch_size", w.batchSize),
		zap.Duration("max_deposit_age", w.maxDepositAge),
	)

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	w.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Deposit allocation recovery worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Deposit allocation recovery worker stopped")
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) reconcile(ctx context.Context) {
	if w.db == nil || w.allocationService == nil {
		return
	}

	candidates, err := w.listUnallocatedDeposits(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("Failed to list unallocated deposits", zap.Error(err))
		return
	}

	if len(candidates) == 0 {
		return
	}

	w.logger.Info("Found unallocated deposits", zap.Int("count", len(candidates)))

	recovered := 0
	for _, candidate := range candidates {
		txHash := candidate.TxHash
		req := &entities.IncomingFundsRequest{
			UserID:     candidate.UserID,
			Amount:     candidate.Amount,
			EventType:  entities.AllocationEventTypeCryptoDeposit,
			DepositID:  &candidate.ID,
			SourceTxID: &txHash,
			Metadata: map[string]any{
				"source":   "deposit_allocation_recovery_worker",
				"recovery": true,
				"chain":    candidate.Chain,
				"token":    candidate.Token,
				"tx_hash":  candidate.TxHash,
			},
		}

		if err := w.allocationService.ProcessIncomingFunds(ctx, req); err != nil {
			w.logger.Error("Failed to recover allocation for deposit",
				zap.String("deposit_id", candidate.ID.String()),
				zap.String("user_id", candidate.UserID.String()),
				zap.Error(err),
			)
			continue
		}

		recovered++
	}

	w.logger.Info("Deposit allocation recovery run completed",
		zap.Int("candidates", len(candidates)),
		zap.Int("recovered", recovered),
	)
}

func (w *Worker) listUnallocatedDeposits(ctx context.Context, limit int) ([]depositCandidate, error) {
	cutoff := time.Now().Add(-w.maxDepositAge)

	const query = `
		SELECT
			d.id,
			d.user_id,
			d.amount,
			d.tx_hash,
			d.chain,
			d.token,
			d.created_at
		FROM deposits d
		INNER JOIN ledger_accounts la
			ON la.user_id = d.user_id
			AND la.account_type = 'usdc_balance'
			AND la.balance >= d.amount
		LEFT JOIN ledger_transactions lt
			ON lt.reference_id = d.id
			AND lt.reference_type = 'allocation_split'
		WHERE d.status IN ('confirmed', 'off_ramp_initiated', 'off_ramp_completed', 'broker_funded')
			AND EXISTS (
				SELECT 1
				FROM ledger_transactions dep_lt
				WHERE dep_lt.reference_id = d.id
					AND dep_lt.reference_type = 'deposit'
			)
			AND d.created_at >= $2
			AND lt.id IS NULL
		ORDER BY d.created_at ASC
		LIMIT $1
	`

	rows, err := w.db.QueryContext(ctx, query, limit, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query unallocated deposits: %w", err)
	}
	defer rows.Close()

	candidates := make([]depositCandidate, 0, limit)
	for rows.Next() {
		var c depositCandidate
		if err := rows.Scan(&c.ID, &c.UserID, &c.Amount, &c.TxHash, &c.Chain, &c.Token, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan unallocated deposit: %w", err)
		}
		candidates = append(candidates, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unallocated deposits: %w", err)
	}

	return candidates, nil
}
