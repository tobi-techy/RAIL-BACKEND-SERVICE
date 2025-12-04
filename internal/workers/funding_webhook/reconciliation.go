package funding_webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// ReconciliationConfig holds configuration for reconciliation worker
type ReconciliationConfig struct {
	Enabled        bool
	Interval       time.Duration
	Threshold      time.Duration
	BatchSize      int
	MaxConcurrency int
}

// DefaultReconciliationConfig returns default configuration
func DefaultReconciliationConfig() ReconciliationConfig {
	return ReconciliationConfig{
		Enabled:        true,
		Interval:       10 * time.Minute,
		Threshold:      15 * time.Minute,
		BatchSize:      50,
		MaxConcurrency: 10,
	}
}

// Reconciler handles reconciliation of stuck deposits
type Reconciler struct {
	config      ReconciliationConfig
	jobRepo     *repositories.FundingEventJobRepository
	depositRepo *repositories.DepositRepository
	fundingSvc  *funding.Service
	validator   *ChainValidator
	auditSvc    *adapters.AuditService
	logger      *logger.Logger

	// Metrics
	meter             metric.Meter
	runsCounter       metric.Int64Counter
	recoveredCounter  metric.Int64Counter
	failedCounter     metric.Int64Counter
	durationHistogram metric.Float64Histogram

	// Worker management
	wg             sync.WaitGroup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewReconciler creates a new reconciliation worker
func NewReconciler(
	config ReconciliationConfig,
	jobRepo *repositories.FundingEventJobRepository,
	depositRepo *repositories.DepositRepository,
	fundingSvc *funding.Service,
	validator *ChainValidator,
	auditSvc *adapters.AuditService,
	logger *logger.Logger,
) (*Reconciler, error) {
	ctx, cancel := context.WithCancel(context.Background())

	meter := otel.Meter("funding-reconciliation")

	// Initialize metrics
	runsCounter, err := meter.Int64Counter(
		"reconciliation.runs.total",
		metric.WithDescription("Total number of reconciliation runs"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create runs counter: %w", err)
	}

	recoveredCounter, err := meter.Int64Counter(
		"reconciliation.recovered.total",
		metric.WithDescription("Total number of recovered deposits"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create recovered counter: %w", err)
	}

	failedCounter, err := meter.Int64Counter(
		"reconciliation.failed.total",
		metric.WithDescription("Total number of failed reconciliations"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create failed counter: %w", err)
	}

	durationHistogram, err := meter.Float64Histogram(
		"reconciliation.duration.seconds",
		metric.WithDescription("Reconciliation duration in seconds"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create duration histogram: %w", err)
	}

	return &Reconciler{
		config:            config,
		jobRepo:           jobRepo,
		depositRepo:       depositRepo,
		fundingSvc:        fundingSvc,
		validator:         validator,
		auditSvc:          auditSvc,
		logger:            logger,
		meter:             meter,
		runsCounter:       runsCounter,
		recoveredCounter:  recoveredCounter,
		failedCounter:     failedCounter,
		durationHistogram: durationHistogram,
		shutdownCtx:       ctx,
		shutdownCancel:    cancel,
	}, nil
}

// Start begins the reconciliation worker
func (r *Reconciler) Start(ctx context.Context) error {
	if !r.config.Enabled {
		r.logger.Info("Reconciliation worker is disabled")
		return nil
	}

	r.logger.Info("Starting reconciliation worker",
		"interval", r.config.Interval,
		"threshold", r.config.Threshold,
		"batch_size", r.config.BatchSize,
	)

	r.wg.Add(1)
	go r.reconciliationLoop(ctx)

	r.logger.Info("Reconciliation worker started successfully")
	return nil
}

// Shutdown gracefully stops the reconciler
func (r *Reconciler) Shutdown(timeout time.Duration) error {
	r.logger.Info("Shutting down reconciliation worker", "timeout", timeout)

	r.shutdownCancel()

	// Wait for worker to finish with timeout
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		r.logger.Info("Reconciliation worker shutdown complete")
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// reconciliationLoop runs reconciliation periodically
func (r *Reconciler) reconciliationLoop(ctx context.Context) {
	defer r.wg.Done()

	// Run immediately on start
	r.runReconciliation(ctx)

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Reconciliation worker stopping")
			return
		case <-r.shutdownCtx.Done():
			r.logger.Info("Reconciliation worker stopping due to shutdown")
			return
		case <-ticker.C:
			r.runReconciliation(ctx)
		}
	}
}

// runReconciliation performs a reconciliation pass
func (r *Reconciler) runReconciliation(ctx context.Context) {
	startTime := time.Now()

	r.logger.Info("Starting reconciliation run", "threshold", r.config.Threshold)

	// Increment runs counter
	r.runsCounter.Add(ctx, 1)

	// Audit log: reconciliation started
	r.auditSvc.LogAction(ctx, nil, "start_reconciliation", "funding_reconciliation", map[string]interface{}{
		"threshold":  r.config.Threshold.String(),
		"batch_size": r.config.BatchSize,
	}, nil)

	// Get pending deposits for reconciliation
	// Validate threshold to prevent timestamp out of range errors
	threshold := r.config.Threshold
	if threshold > 24*365*10*time.Hour { // Cap at 10 years to prevent overflow
		r.logger.Warn("Threshold too large, capping at 10 years", "original", threshold)
		threshold = 24*365*10*time.Hour
	}
	
	candidates, err := r.jobRepo.GetPendingDepositsForReconciliation(ctx, threshold, r.config.BatchSize)
	if err != nil {
		r.logger.Error("Failed to get reconciliation candidates", "error", err)
		r.failedCounter.Add(ctx, 1)
		return
	}

	if len(candidates) == 0 {
		r.logger.Debug("No deposits to reconcile")
		duration := time.Since(startTime)
		r.durationHistogram.Record(ctx, duration.Seconds())
		return
	}

	r.logger.Info("Found deposits to reconcile", "count", len(candidates))

	// Process candidates concurrently
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.config.MaxConcurrency) // Semaphore for concurrency control

	recoveredCount := 0
	failedCount := 0
	var mu sync.Mutex

	for _, candidate := range candidates {
		wg.Add(1)
		go func(c *entities.ReconciliationCandidate) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			if r.reconcileDeposit(ctx, c) {
				mu.Lock()
				recoveredCount++
				mu.Unlock()
			} else {
				mu.Lock()
				failedCount++
				mu.Unlock()
			}
		}(candidate)
	}

	wg.Wait()

	duration := time.Since(startTime)

	// Record metrics
	r.recoveredCounter.Add(ctx, int64(recoveredCount))
	r.failedCounter.Add(ctx, int64(failedCount))
	r.durationHistogram.Record(ctx, duration.Seconds())

	r.logger.Info("Reconciliation run completed",
		"duration", duration,
		"total_candidates", len(candidates),
		"recovered", recoveredCount,
		"failed", failedCount,
	)

	// Audit log: reconciliation completed
	r.auditSvc.LogAction(ctx, nil, "complete_reconciliation", "funding_reconciliation", map[string]interface{}{
		"duration_seconds": duration.Seconds(),
		"total_candidates": len(candidates),
		"recovered":        recoveredCount,
		"failed":           failedCount,
	}, nil)
}

// reconcileDeposit attempts to reconcile a single deposit
func (r *Reconciler) reconcileDeposit(ctx context.Context, candidate *entities.ReconciliationCandidate) bool {
	r.logger.Info("Reconciling deposit",
		"deposit_id", candidate.DepositID,
		"tx_hash", candidate.TxHash,
		"chain", candidate.Chain,
		"pending_duration", candidate.PendingDuration,
	)

	// Validate transaction on-chain
	status, err := r.validator.ValidateTransaction(ctx, candidate.Chain, candidate.TxHash)
	if err != nil {
		r.logger.Error("Failed to validate transaction on-chain",
			"error", err,
			"tx_hash", candidate.TxHash,
			"chain", candidate.Chain,
		)
		return false
	}

	switch status {
	case TransactionStatusConfirmed:
		// Transaction confirmed, process it
		r.logger.Info("Transaction confirmed on-chain, processing",
			"tx_hash", candidate.TxHash,
			"chain", candidate.Chain,
		)

		webhook := &entities.ChainDepositWebhook{
			Chain:     candidate.Chain,
			TxHash:    candidate.TxHash,
			Token:     candidate.Token,
			Amount:    candidate.Amount.String(),
			Address:   candidate.ToAddress,
			BlockTime: candidate.CreatedAt,
		}

		err := r.fundingSvc.ProcessChainDeposit(ctx, webhook)
		if err != nil {
			r.logger.Error("Failed to process confirmed deposit",
				"error", err,
				"tx_hash", candidate.TxHash,
			)
			return false
		}

		r.logger.Info("Successfully recovered deposit",
			"tx_hash", candidate.TxHash,
			"user_id", candidate.UserID,
		)

		// Audit log: deposit recovered
		r.auditSvc.LogAction(ctx, &candidate.UserID, "recover_deposit", "deposit", map[string]interface{}{
			"deposit_id": candidate.DepositID.String(),
			"tx_hash":    candidate.TxHash,
			"chain":      candidate.Chain,
			"amount":     candidate.Amount.String(),
		}, nil)

		return true

	case TransactionStatusFailed:
		// Transaction failed on-chain, mark deposit as failed
		r.logger.Warn("Transaction failed on-chain, marking deposit as failed",
			"tx_hash", candidate.TxHash,
			"chain", candidate.Chain,
		)

		now := time.Now()
		err := r.depositRepo.UpdateStatus(ctx, candidate.DepositID, "failed", &now)
		if err != nil {
			r.logger.Error("Failed to update deposit status",
				"error", err,
				"deposit_id", candidate.DepositID,
			)
			return false
		}

		// Audit log: deposit marked as failed
		r.auditSvc.LogAction(ctx, &candidate.UserID, "mark_deposit_failed", "deposit", map[string]interface{}{
			"deposit_id": candidate.DepositID.String(),
			"tx_hash":    candidate.TxHash,
			"reason":     "transaction_failed_on_chain",
		}, nil)

		return true

	case TransactionStatusPending:
		// Still pending, log and skip
		r.logger.Debug("Transaction still pending on-chain",
			"tx_hash", candidate.TxHash,
			"chain", candidate.Chain,
		)
		return false

	case TransactionStatusNotFound:
		// Transaction not found - might be too old or invalid
		r.logger.Warn("Transaction not found on-chain",
			"tx_hash", candidate.TxHash,
			"chain", candidate.Chain,
		)

		// If pending for too long (e.g., > 1 hour), mark as failed
		if candidate.PendingDuration > 1*time.Hour {
			r.logger.Warn("Marking long-pending deposit as failed",
				"tx_hash", candidate.TxHash,
				"pending_duration", candidate.PendingDuration,
			)

			now := time.Now()
			err := r.depositRepo.UpdateStatus(ctx, candidate.DepositID, "failed", &now)
			if err != nil {
				r.logger.Error("Failed to update deposit status",
					"error", err,
					"deposit_id", candidate.DepositID,
				)
				return false
			}

			return true
		}

		return false

	default:
		r.logger.Warn("Unknown transaction status",
			"status", status,
			"tx_hash", candidate.TxHash,
		)
		return false
	}
}

// TransactionStatus represents the status of a transaction on-chain
type TransactionStatus string

const (
	TransactionStatusConfirmed TransactionStatus = "confirmed"
	TransactionStatusFailed    TransactionStatus = "failed"
	TransactionStatusPending   TransactionStatus = "pending"
	TransactionStatusNotFound  TransactionStatus = "not_found"
)

// ChainValidator validates transactions on-chain
type ChainValidator struct {
	logger *logger.Logger
	// TODO: Add RPC clients for different chains
	// solanaClient *solana.Client
	// evmClients   map[string]*ethclient.Client
}

// NewChainValidator creates a new chain validator
func NewChainValidator(logger *logger.Logger) *ChainValidator {
	return &ChainValidator{
		logger: logger,
	}
}

// ValidateTransaction validates a transaction on the specified chain
func (v *ChainValidator) ValidateTransaction(ctx context.Context, chain entities.Chain, txHash string) (TransactionStatus, error) {
	v.logger.Debug("Validating transaction", "chain", chain, "tx_hash", txHash)

	switch chain {
	case entities.ChainSolana:
		return v.validateSolanaTransaction(ctx, txHash)
	case entities.ChainAptos:
		return v.validateAptosTransaction(ctx, txHash)
	case entities.ChainPolygon:
		return v.validateEVMTransaction(ctx, "polygon", txHash)
	case entities.ChainStarknet:
		return v.validateStarknetTransaction(ctx, txHash)
	default:
		return TransactionStatusNotFound, fmt.Errorf("unsupported chain: %s", chain)
	}
}

// validateSolanaTransaction validates a Solana transaction
func (v *ChainValidator) validateSolanaTransaction(ctx context.Context, txHash string) (TransactionStatus, error) {
	// TODO: Implement Solana RPC validation
	// Example implementation:
	// 1. Call getTransaction(txHash) with commitment "finalized"
	// 2. Check transaction.meta.err field
	// 3. If err is null, transaction succeeded
	// 4. If err is not null, transaction failed
	// 5. If transaction not found, return NotFound

	v.logger.Debug("Solana validation not implemented yet", "tx_hash", txHash)

	// Placeholder: return pending to avoid false negatives
	return TransactionStatusPending, nil
}

// validateAptosTransaction validates an Aptos transaction
func (v *ChainValidator) validateAptosTransaction(ctx context.Context, txHash string) (TransactionStatus, error) {
	// TODO: Implement Aptos REST API validation
	// Example: GET https://fullnode.mainnet.aptoslabs.com/v1/transactions/by_hash/{txHash}

	v.logger.Debug("Aptos validation not implemented yet", "tx_hash", txHash)
	return TransactionStatusPending, nil
}

// validateEVMTransaction validates an EVM chain transaction (Polygon, etc.)
func (v *ChainValidator) validateEVMTransaction(ctx context.Context, network string, txHash string) (TransactionStatus, error) {
	// TODO: Implement EVM RPC validation
	// Example using ethclient:
	// 1. Call eth_getTransactionReceipt(txHash)
	// 2. Check receipt.Status (1 = success, 0 = failed)
	// 3. Check receipt.BlockNumber is not null (confirmed)
	// 4. If receipt is null, transaction not found or pending

	v.logger.Debug("EVM validation not implemented yet", "network", network, "tx_hash", txHash)
	return TransactionStatusPending, nil
}

// validateStarknetTransaction validates a Starknet transaction
func (v *ChainValidator) validateStarknetTransaction(ctx context.Context, txHash string) (TransactionStatus, error) {
	// TODO: Implement Starknet RPC validation

	v.logger.Debug("Starknet validation not implemented yet", "tx_hash", txHash)
	return TransactionStatusPending, nil
}
