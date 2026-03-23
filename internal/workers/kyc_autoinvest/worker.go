package kyc_autoinvest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
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
	StashUpdatedAt time.Time
}

// Worker periodically triggers auto-invest for KYC-approved users with stash balances.
type Worker struct {
	db                *sql.DB
	autoInvestService AutoInvestService
	logger            *zap.Logger
	checkInterval     time.Duration
	batchSize         int
	stopCh            chan struct{}
	stopOnce          sync.Once
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

// Stop signals the worker to stop. Safe to call multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

func (w *Worker) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Panic in kyc_autoinvest worker run", zap.Any("panic", r))
		}
	}()

	if w.db == nil || w.autoInvestService == nil {
		return
	}

	// Skip entirely when the market is closed — orders would be deferred anyway
	// and this avoids unnecessary DB queries and log noise.
	if !isMarketOpen() {
		w.logger.Debug("kyc_autoinvest worker skipping run — market is closed")
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
		// Deterministic correlation ID: same user + account + ledger timestamp + balance = same ID.
		// StashUpdatedAt advances only when the ledger balance changes, so the key is stable
		// within a single balance state and rotates naturally on each new deposit.
		// The DB unique index is the concurrent-execution guard.
		correlationID := fmt.Sprintf(
			"kyc-autoinvest:%s:%s:%s:%s",
			candidate.UserID.String(),
			candidate.StashAccountID.String(),
			candidate.StashUpdatedAt.UTC().Format(time.RFC3339),
			candidate.StashBalance.StringFixed(2),
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

// isMarketOpen returns true when the US equity market is currently open (Mon–Fri 09:30–16:00 ET).
func isMarketOpen() bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return true // fail open
	}
	et := time.Now().In(loc)
	wd := et.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)
	return et.After(open) && et.Before(close)
}

func (w *Worker) listCandidates(ctx context.Context, limit int) ([]investCandidate, error) {
	const query = `
		SELECT DISTINCT ON (la.user_id)
			la.user_id,
			la.id         AS stash_account_id,
			la.balance    AS stash_balance,
			la.updated_at AS stash_updated_at
		FROM ledger_accounts la
		INNER JOIN users u ON u.id = la.user_id
		WHERE la.account_type = 'stash_balance'
			AND (
				la.balance >= 1.00
				OR EXISTS (
					SELECT 1 FROM ledger_accounts fe
					WHERE fe.user_id = la.user_id
						AND fe.account_type = 'fiat_exposure'
						AND fe.balance >= 1.00
				)
			)
			AND u.kyc_status = 'approved'
			AND u.bridge_kyc_status = 'active'
			AND u.is_active = true
			AND u.alpaca_account_id IS NOT NULL
		ORDER BY la.user_id, la.updated_at ASC
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
		if err := rows.Scan(&c.UserID, &c.StashAccountID, &c.StashBalance, &c.StashUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan KYC auto-invest candidate: %w", err)
		}
		candidates = append(candidates, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate KYC auto-invest candidates: %w", err)
	}

	return candidates, nil
}
