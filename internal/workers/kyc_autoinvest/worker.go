package kyc_autoinvest

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AutoInvestService defines auto-invest operations used by this worker.
type AutoInvestService interface {
	TriggerAutoInvestment(ctx context.Context, req autoinvest.TriggerRequest) error
}

// Config controls KYC auto-invest worker behavior.
type Config struct {
	CheckInterval time.Duration
	BatchSize     int
}

// DefaultConfig returns default periodic settings.
func DefaultConfig() *Config {
	return &Config{
		CheckInterval: 30 * time.Second,
		BatchSize:     100,
	}
}

type investCandidate struct {
	UserID         uuid.UUID
	StashAccountID uuid.UUID
	StashBalance   decimal.Decimal
}

// Worker periodically triggers auto-invest for KYC-approved users with stash balances.
type Worker struct {
	db                *sql.DB
	autoInvestService AutoInvestService
	logger            *zap.Logger
	checkInterval     time.Duration
	batchSize         int
	stopCh            chan struct{}
}

// NewWorker creates a new KYC auto-invest worker.
func NewWorker(db *sql.DB, autoInvestService AutoInvestService, logger *zap.Logger, config *Config) *Worker {
	if config == nil {
		config = DefaultConfig()
	}
	if config.CheckInterval <= 0 {
		config.CheckInterval = DefaultConfig().CheckInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultConfig().BatchSize
	}

	return &Worker{
		db:                db,
		autoInvestService: autoInvestService,
		logger:            logger,
		checkInterval:     config.CheckInterval,
		batchSize:         config.BatchSize,
		stopCh:            make(chan struct{}),
	}
}

// Start begins periodic auto-invest checks.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting KYC auto-invest worker",
		zap.Duration("check_interval", w.checkInterval),
		zap.Int("batch_size", w.batchSize),
	)

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	w.run(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("KYC auto-invest worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("KYC auto-invest worker stopped")
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) run(ctx context.Context) {
	if w.db == nil || w.autoInvestService == nil {
		return
	}

	candidates, err := w.listCandidates(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("Failed to list KYC auto-invest candidates", zap.Error(err))
		return
	}

	if len(candidates) == 0 {
		return
	}

	w.logger.Info("Found KYC auto-invest candidates", zap.Int("count", len(candidates)))

	triggered := 0
	for _, candidate := range candidates {
		correlationID := fmt.Sprintf(
			"kyc-approved-auto-invest:%s:%s:%s",
			candidate.UserID.String(),
			candidate.StashAccountID.String(),
			candidate.StashBalance.String(),
		)

		if err := w.autoInvestService.TriggerAutoInvestment(ctx, autoinvest.TriggerRequest{
			UserID:        candidate.UserID,
			StashID:       candidate.StashAccountID,
			CorrelationID: correlationID,
		}); err != nil {
			w.logger.Error("Failed to trigger KYC auto-invest",
				zap.String("user_id", candidate.UserID.String()),
				zap.String("stash_account_id", candidate.StashAccountID.String()),
				zap.String("stash_balance", candidate.StashBalance.String()),
				zap.Error(err),
			)
			continue
		}

		triggered++
	}

	w.logger.Info("KYC auto-invest run completed",
		zap.Int("candidates", len(candidates)),
		zap.Int("triggered", triggered),
	)
}

func (w *Worker) listCandidates(ctx context.Context, limit int) ([]investCandidate, error) {
	const query = `
		SELECT
			la.user_id,
			la.id,
			la.balance
		FROM ledger_accounts la
		INNER JOIN users u ON u.id = la.user_id
		WHERE la.account_type = 'stash_balance'
			AND la.balance > 0
			AND u.kyc_status = 'approved'
			AND u.bridge_kyc_status = 'active'
			AND u.is_active = true
			AND u.alpaca_account_id IS NOT NULL
		ORDER BY la.updated_at ASC
		LIMIT $1
	`

	rows, err := w.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query KYC auto-invest candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]investCandidate, 0, limit)
	for rows.Next() {
		var c investCandidate
		if err := rows.Scan(&c.UserID, &c.StashAccountID, &c.StashBalance); err != nil {
			return nil, fmt.Errorf("scan KYC auto-invest candidate: %w", err)
		}
		candidates = append(candidates, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate KYC auto-invest candidates: %w", err)
	}

	return candidates, nil
}
