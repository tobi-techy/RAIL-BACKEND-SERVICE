package treasury

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Engine orchestrates treasury operations: buffer management, conversions, and net settlement
type Engine struct {
	ledgerService   *ledger.Service
	treasuryRepo    *repositories.TreasuryRepository
	providerFactory ProviderFactory
	providers       map[string]ConversionProvider
	db              *sqlx.DB
	logger          *logger.Logger
	config          *EngineConfig
}

// EngineConfig holds treasury engine configuration
type EngineConfig struct {
	SchedulerInterval       time.Duration
	BatchWindow             time.Duration
	MaxRetries              int
	RetryBackoffMultiplier  float64
	HealthCheckInterval     time.Duration
	ConversionTimeout       time.Duration
	EnableAutoRebalance     bool
	EmergencyThresholdRatio float64 // Trigger emergency if below this ratio of min threshold
}

// NewEngine creates a new treasury engine
func NewEngine(
	ledgerService *ledger.Service,
	treasuryRepo *repositories.TreasuryRepository,
	providerFactory ProviderFactory,
	db *sqlx.DB,
	logger *logger.Logger,
	config *EngineConfig,
) *Engine {
	if config == nil {
		config = DefaultEngineConfig()
	}

	return &Engine{
		ledgerService:   ledgerService,
		treasuryRepo:    treasuryRepo,
		providerFactory: providerFactory,
		providers:       make(map[string]ConversionProvider),
		db:              db,
		logger:          logger,
		config:          config,
	}
}

// DefaultEngineConfig returns default configuration
func DefaultEngineConfig() *EngineConfig {
	return &EngineConfig{
		SchedulerInterval:       5 * time.Minute,
		BatchWindow:             5 * time.Minute,
		MaxRetries:              3,
		RetryBackoffMultiplier:  2.0,
		HealthCheckInterval:     1 * time.Minute,
		ConversionTimeout:       30 * time.Minute,
		EnableAutoRebalance:     true,
		EmergencyThresholdRatio: 0.5, // Alert if below 50% of min threshold
	}
}

// Initialize loads conversion providers and prepares the engine
func (e *Engine) Initialize(ctx context.Context) error {
	e.logger.Info("Initializing Treasury Engine")

	// Load all active providers from database
	providerConfigs, err := e.treasuryRepo.GetActiveProviders(ctx)
	if err != nil {
		return fmt.Errorf("failed to load providers: %w", err)
	}

	// Create provider adapters
	for _, config := range providerConfigs {
		provider, err := e.providerFactory.CreateProvider(*config)
		if err != nil {
			e.logger.Warn("Failed to create provider adapter",
				"provider", config.Name,
				"error", err)
			continue
		}
		e.providers[config.ProviderType] = provider
		e.logger.Info("Loaded conversion provider",
			"name", config.Name,
			"type", config.ProviderType,
			"priority", config.Priority)
	}

	if len(e.providers) == 0 {
		return fmt.Errorf("no conversion providers available")
	}

	e.logger.Info("Treasury Engine initialized successfully",
		"providers", len(e.providers))

	return nil
}

// RunNetSettlementCycle executes one cycle of net settlement
// This is the main orchestration method called by the scheduler
func (e *Engine) RunNetSettlementCycle(ctx context.Context) error {
	e.logger.Info("Starting net settlement cycle")

	// 1. Check buffer levels
	bufferStatuses, err := e.CheckBufferLevels(ctx)
	if err != nil {
		return fmt.Errorf("failed to check buffer levels: %w", err)
	}

	// 2. Process any stuck/stale conversion jobs
	if err := e.ProcessStaleJobs(ctx); err != nil {
		e.logger.Error("Failed to process stale jobs", "error", err)
		// Don't fail the cycle, just log
	}

	// 3. Create conversion jobs for buffers that need replenishment
	var jobsCreated int
	for _, status := range bufferStatuses {
		if !status.NeedsReplenishment() {
			continue
		}

		e.logger.Warn("Buffer needs replenishment",
			"account_type", status.AccountType,
			"current_balance", status.CurrentBalance,
			"min_threshold", status.MinThreshold,
			"target_threshold", status.TargetThreshold,
			"health_status", status.HealthStatus)

		// Determine conversion direction and amount
		conversionJob, err := e.CreateReplenishmentJob(ctx, status)
		if err != nil {
			e.logger.Error("Failed to create replenishment job",
				"account_type", status.AccountType,
				"error", err)
			continue
		}

		if conversionJob != nil {
			jobsCreated++
			e.logger.Info("Created conversion job",
				"job_id", conversionJob.ID,
				"direction", conversionJob.Direction,
				"amount", conversionJob.Amount)
		}
	}

	// 4. Execute pending conversion jobs
	pendingJobs, err := e.treasuryRepo.GetJobsByStatus(ctx, entities.ConversionJobStatusPending)
	if err != nil {
		return fmt.Errorf("failed to get pending jobs: %w", err)
	}

	for _, job := range pendingJobs {
		if err := e.ExecuteConversionJob(ctx, job); err != nil {
			e.logger.Error("Failed to execute conversion job",
				"job_id", job.ID,
				"error", err)
			// Continue with other jobs
		}
	}

	e.logger.Info("Net settlement cycle completed",
		"jobs_created", jobsCreated,
		"pending_jobs", len(pendingJobs))

	return nil
}

// CheckBufferLevels checks all system buffer account levels against thresholds
func (e *Engine) CheckBufferLevels(ctx context.Context) ([]*entities.BufferStatus, error) {
	// Get all buffer thresholds
	thresholds, err := e.treasuryRepo.GetAllBufferThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buffer thresholds: %w", err)
	}

	var statuses []*entities.BufferStatus

	for _, threshold := range thresholds {
		// Get current balance from ledger
		systemAccount, err := e.ledgerService.GetSystemAccount(ctx, threshold.AccountType)
		if err != nil {
			e.logger.Error("Failed to get system account balance",
				"account_type", threshold.AccountType,
				"error", err)
			continue
		}

		// Calculate health status
		healthStatus := threshold.CheckHealthStatus(systemAccount.Balance)
		amountToTarget := decimal.Zero
		if systemAccount.Balance.LessThan(threshold.TargetThreshold) {
			amountToTarget = threshold.TargetThreshold.Sub(systemAccount.Balance)
		}

		status := &entities.BufferStatus{
			AccountType:     threshold.AccountType,
			CurrentBalance:  systemAccount.Balance,
			MinThreshold:    threshold.MinThreshold,
			TargetThreshold: threshold.TargetThreshold,
			MaxThreshold:    threshold.MaxThreshold,
			HealthStatus:    healthStatus,
			AmountToTarget:  amountToTarget,
		}

		statuses = append(statuses, status)

		// Log critical situations
		if healthStatus == entities.BufferHealthCriticalLow {
			e.logger.Error("CRITICAL: Buffer critically low",
				"account_type", threshold.AccountType,
				"current", systemAccount.Balance,
				"min_threshold", threshold.MinThreshold)
		}
	}

	return statuses, nil
}

// CreateReplenishmentJob creates a conversion job to replenish a buffer
func (e *Engine) CreateReplenishmentJob(ctx context.Context, status *entities.BufferStatus) (*entities.ConversionJob, error) {
	// Determine conversion direction based on account type
	var direction entities.ConversionDirection
	var sourceAccountType, destAccountType entities.AccountType

	switch status.AccountType {
	case entities.AccountTypeSystemBufferUSDC:
		// Need more USDC on-chain: USD -> USDC
		direction = entities.ConversionDirectionUSDToUSDC
		sourceAccountType = entities.AccountTypeSystemBufferFiat
		destAccountType = entities.AccountTypeSystemBufferUSDC

	case entities.AccountTypeSystemBufferFiat:
		// Need more USD at conversion provider: USDC -> USD
		direction = entities.ConversionDirectionUSDCToUSD
		sourceAccountType = entities.AccountTypeSystemBufferUSDC
		destAccountType = entities.AccountTypeSystemBufferFiat

	case entities.AccountTypeBrokerOperational:
		// Need more USD at Alpaca: USDC -> USD -> Alpaca
		direction = entities.ConversionDirectionUSDCToUSD
		sourceAccountType = entities.AccountTypeSystemBufferUSDC
		destAccountType = entities.AccountTypeBrokerOperational

	default:
		return nil, fmt.Errorf("unsupported account type for replenishment: %s", status.AccountType)
	}

	// Get source and destination accounts
	sourceAccount, err := e.ledgerService.GetSystemAccount(ctx, sourceAccountType)
	if err != nil {
		return nil, fmt.Errorf("failed to get source account: %w", err)
	}

	destAccount, err := e.ledgerService.GetSystemAccount(ctx, destAccountType)
	if err != nil {
		return nil, fmt.Errorf("failed to get destination account: %w", err)
	}

	// Calculate replenishment amount (to target threshold)
	amount := status.AmountToTarget
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, nil // Nothing to do
	}

	// Check source account has sufficient balance
	if sourceAccount.Balance.LessThan(amount) {
		e.logger.Warn("Source account has insufficient balance for replenishment",
			"source_account", sourceAccountType,
			"available", sourceAccount.Balance,
			"needed", amount)
		// Use available balance instead
		amount = sourceAccount.Balance
	}

	// Determine trigger reason
	triggerReason := entities.ConversionTriggerBufferReplenishment
	if status.HealthStatus == entities.BufferHealthCriticalLow {
		triggerReason = entities.ConversionTriggerEmergency
	}

	// Create conversion job
	idempotencyKey := fmt.Sprintf("replenish-%s-%d", status.AccountType, time.Now().UnixNano())
	notes := fmt.Sprintf("Replenish %s buffer to target threshold", status.AccountType)

	req := &entities.CreateConversionJobRequest{
		Direction:            direction,
		Amount:               amount,
		TriggerReason:        triggerReason,
		SourceAccountID:      sourceAccount.ID,
		DestinationAccountID: destAccount.ID,
		IdempotencyKey:       idempotencyKey,
		Notes:                &notes,
	}

	job, err := e.treasuryRepo.CreateConversionJob(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create conversion job: %w", err)
	}

	return job, nil
}

// ExecuteConversionJob executes a pending conversion job
func (e *Engine) ExecuteConversionJob(ctx context.Context, job *entities.ConversionJob) error {
	e.logger.Info("Executing conversion job",
		"job_id", job.ID,
		"direction", job.Direction,
		"amount", job.Amount)

	// Select best provider for this conversion
	providerConfigs, err := e.treasuryRepo.GetActiveProviders(ctx)
	if err != nil {
		return fmt.Errorf("failed to get providers: %w", err)
	}

	selector := NewProviderSelector(providerConfigs)
	selectedConfig, err := selector.SelectProvider(job.Amount, job.Direction)
	if err != nil {
		return e.markJobFailed(ctx, job, fmt.Sprintf("no available provider: %v", err), "NO_PROVIDER")
	}

	// Get provider adapter
	provider, exists := e.providers[selectedConfig.ProviderType]
	if !exists {
		return e.markJobFailed(ctx, job, "provider adapter not initialized", "PROVIDER_NOT_INITIALIZED")
	}

	// Validate amount with provider
	if err := provider.ValidateAmount(job.Amount, job.Direction); err != nil {
		return e.markJobFailed(ctx, job, fmt.Sprintf("amount validation failed: %v", err), "INVALID_AMOUNT")
	}

	// Prepare conversion request
	sourceCurrency := "USDC"
	destCurrency := "USD"
	if job.Direction == entities.ConversionDirectionUSDToUSDC {
		sourceCurrency = "USD"
		destCurrency = "USDC"
	}

	convReq := &ConversionRequest{
		Direction:           job.Direction,
		SourceAmount:        job.Amount,
		SourceCurrency:      sourceCurrency,
		DestinationCurrency: destCurrency,
		IdempotencyKey:      *job.IdempotencyKey,
		Metadata: map[string]interface{}{
			"job_id":       job.ID.String(),
			"trigger":      job.TriggerReason,
			"source_account": job.SourceAccountID.String(),
			"dest_account":   job.DestinationAccountID.String(),
		},
	}

	// Initiate conversion with provider
	convResp, err := provider.InitiateConversion(ctx, convReq)
	if err != nil {
		retryable := IsRetryable(err)
		if retryable && job.CanRetry() {
			return e.markJobForRetry(ctx, job, err.Error())
		}
		return e.markJobFailed(ctx, job, err.Error(), "PROVIDER_ERROR")
	}

	// Update job with provider response
	providerRespJSON, _ := json.Marshal(convResp.ProviderResponse)
	providerRespStr := string(providerRespJSON)

	updateReq := &entities.UpdateConversionJobStatusRequest{
		JobID:            job.ID,
		NewStatus:        entities.ConversionJobStatusProviderSubmitted,
		ProviderTxID:     &convResp.ProviderTxID,
		ProviderResponse: &providerRespStr,
	}

	if err := e.treasuryRepo.UpdateConversionJobStatus(ctx, updateReq); err != nil {
		e.logger.Error("Failed to update job status after provider submission",
			"job_id", job.ID,
			"error", err)
		// Don't fail - provider has the job
	}

	// Update job in memory
	job.Status = entities.ConversionJobStatusProviderSubmitted
	job.ProviderID = &selectedConfig.ID
	providerName := selectedConfig.Name
	job.ProviderName = &providerName
	job.ProviderTxID = &convResp.ProviderTxID
	now := time.Now()
	job.SubmittedAt = &now

	e.logger.Info("Conversion job submitted to provider",
		"job_id", job.ID,
		"provider", selectedConfig.Name,
		"provider_tx_id", convResp.ProviderTxID)

	return nil
}

// MonitorConversionJobs checks status of in-flight conversion jobs
func (e *Engine) MonitorConversionJobs(ctx context.Context) error {
	// Get all jobs in processing states
	statuses := []entities.ConversionJobStatus{
		entities.ConversionJobStatusProviderSubmitted,
		entities.ConversionJobStatusProviderProcessing,
	}

	for _, status := range statuses {
		jobs, err := e.treasuryRepo.GetJobsByStatus(ctx, status)
		if err != nil {
			e.logger.Error("Failed to get jobs for monitoring",
				"status", status,
				"error", err)
			continue
		}

		for _, job := range jobs {
			if err := e.CheckJobStatus(ctx, job); err != nil {
				e.logger.Error("Failed to check job status",
					"job_id", job.ID,
					"error", err)
			}
		}
	}

	return nil
}

// CheckJobStatus checks the status of a conversion job with the provider
func (e *Engine) CheckJobStatus(ctx context.Context, job *entities.ConversionJob) error {
	if job.ProviderTxID == nil {
		return fmt.Errorf("job has no provider transaction ID")
	}

	// Get provider adapter
	if job.ProviderName == nil {
		return fmt.Errorf("job has no provider name")
	}

	provider, exists := e.providers[*job.ProviderName]
	if !exists {
		return fmt.Errorf("provider not found: %s", *job.ProviderName)
	}

	// Check status with provider
	statusResp, err := provider.GetConversionStatus(ctx, *job.ProviderTxID)
	if err != nil {
		e.logger.Warn("Failed to get conversion status from provider",
			"job_id", job.ID,
			"provider_tx_id", *job.ProviderTxID,
			"error", err)
		return err
	}

	// Update job based on provider status
	newStatus := statusResp.Status.ToJobStatus()

	// If status changed, update the job
	if newStatus != job.Status {
		e.logger.Info("Conversion job status changed",
			"job_id", job.ID,
			"old_status", job.Status,
			"new_status", newStatus,
			"provider_tx_id", *job.ProviderTxID)

		updateReq := &entities.UpdateConversionJobStatusRequest{
			JobID:             job.ID,
			NewStatus:         newStatus,
			SourceAmount:      &statusResp.SourceAmount,
			DestinationAmount: statusResp.DestinationAmount,
			ExchangeRate:      statusResp.ExchangeRate,
			FeesPaid:          statusResp.Fees,
		}

		if statusResp.FailureReason != nil {
			updateReq.ErrorMessage = statusResp.FailureReason
		}

		if err := e.treasuryRepo.UpdateConversionJobStatus(ctx, updateReq); err != nil {
			return fmt.Errorf("failed to update job status: %w", err)
		}

		// If provider completed, post ledger entries
		if newStatus == entities.ConversionJobStatusProviderCompleted {
			if err := e.PostLedgerEntries(ctx, job, statusResp); err != nil {
				e.logger.Error("Failed to post ledger entries",
					"job_id", job.ID,
					"error", err)
				return err
			}
		}
	}

	return nil
}

// PostLedgerEntries creates ledger entries for a completed conversion
func (e *Engine) PostLedgerEntries(ctx context.Context, job *entities.ConversionJob, statusResp *ConversionStatusResponse) error {
	e.logger.Info("Posting ledger entries for completed conversion",
		"job_id", job.ID,
		"direction", job.Direction)

	// Use actual amounts from provider response
	sourceAmount := statusResp.SourceAmount

	destAmount := sourceAmount // 1:1 for stablecoins by default
	if statusResp.DestinationAmount != nil {
		destAmount = *statusResp.DestinationAmount
	}

	// Get source and destination accounts by ID
	sourceAccount, err := e.ledgerService.GetAccountByID(ctx, *job.SourceAccountID)
	if err != nil {
		return fmt.Errorf("failed to get source account: %w", err)
	}

	destAccount, err := e.ledgerService.GetAccountByID(ctx, *job.DestinationAccountID)
	if err != nil {
		return fmt.Errorf("failed to get dest account: %w", err)
	}

	// Determine currencies
	sourceCurrency := "USDC"
	destCurrency := "USD"
	if job.Direction == entities.ConversionDirectionUSDToUSDC {
		sourceCurrency = "USD"
		destCurrency = "USDC"
	}

	// Create ledger transaction
	desc := fmt.Sprintf("Conversion: %s -> %s (Job: %s)", job.Direction, *job.ProviderTxID, job.ID.String())
	metadata := map[string]any{
		"conversion_job_id": job.ID.String(),
		"provider_tx_id":    *job.ProviderTxID,
		"direction":         job.Direction,
		"trigger":           job.TriggerReason,
	}

	ledgerReq := &entities.CreateTransactionRequest{
		UserID:          nil, // System transaction
		TransactionType: entities.TransactionTypeConversion,
		ReferenceID:     &job.ID,
		ReferenceType:   stringPtr("conversion_job"),
		IdempotencyKey:  fmt.Sprintf("conversion-%s", job.ID.String()),
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   sourceAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Deduct from source
				Amount:      sourceAmount,
				Currency:    sourceCurrency,
				Description: &desc,
			},
			{
				AccountID:   destAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Add to destination
				Amount:      destAmount,
				Currency:    destCurrency,
				Description: &desc,
			},
		},
	}

	ledgerTx, err := e.ledgerService.CreateTransaction(ctx, ledgerReq)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	// Update conversion job with ledger transaction ID
	updateReq := &entities.UpdateConversionJobStatusRequest{
		JobID:     job.ID,
		NewStatus: entities.ConversionJobStatusCompleted,
	}

	if err := e.treasuryRepo.UpdateConversionJobStatus(ctx, updateReq); err != nil {
		return fmt.Errorf("failed to mark job as completed: %w", err)
	}

	// Link ledger transaction to conversion job
	if err := e.treasuryRepo.UpdateJobLedgerTransaction(ctx, job.ID, ledgerTx.ID); err != nil {
		e.logger.Error("Failed to link ledger transaction to job",
			"job_id", job.ID,
			"ledger_tx_id", ledgerTx.ID,
			"error", err)
		// Don't fail - transaction is already posted
	}

	e.logger.Info("Ledger entries posted for conversion",
		"job_id", job.ID,
		"ledger_tx_id", ledgerTx.ID,
		"source_amount", sourceAmount,
		"dest_amount", destAmount)

	return nil
}

// ProcessStaleJobs handles jobs that have been stuck in processing for too long
func (e *Engine) ProcessStaleJobs(ctx context.Context) error {
	staleThreshold := time.Now().Add(-e.config.ConversionTimeout)

	staleJobs, err := e.treasuryRepo.GetStaleJobs(ctx, staleThreshold)
	if err != nil {
		return fmt.Errorf("failed to get stale jobs: %w", err)
	}

	for _, job := range staleJobs {
		e.logger.Warn("Found stale conversion job",
			"job_id", job.ID,
			"status", job.Status,
			"submitted_at", job.SubmittedAt)

		// Try to check status one more time
		if err := e.CheckJobStatus(ctx, job); err != nil {
			// If we can't check status and it's been too long, mark as failed
			if job.CanRetry() {
				if err := e.markJobForRetry(ctx, job, "conversion timeout"); err != nil {
					e.logger.Error("Failed to mark stale job for retry",
						"job_id", job.ID,
						"error", err)
				}
			} else {
				if err := e.markJobFailed(ctx, job, "conversion timeout - max retries exceeded", "TIMEOUT"); err != nil {
					e.logger.Error("Failed to mark stale job as failed",
						"job_id", job.ID,
						"error", err)
				}
			}
		}
	}

	return nil
}

// Helper methods

func (e *Engine) markJobFailed(ctx context.Context, job *entities.ConversionJob, errorMsg, errorCode string) error {
	e.logger.Error("Marking conversion job as failed",
		"job_id", job.ID,
		"error", errorMsg,
		"code", errorCode)

	updateReq := &entities.UpdateConversionJobStatusRequest{
		JobID:        job.ID,
		NewStatus:    entities.ConversionJobStatusFailed,
		ErrorMessage: &errorMsg,
		ErrorCode:    &errorCode,
	}

	return e.treasuryRepo.UpdateConversionJobStatus(ctx, updateReq)
}

func (e *Engine) markJobForRetry(ctx context.Context, job *entities.ConversionJob, errorMsg string) error {
	e.logger.Warn("Marking conversion job for retry",
		"job_id", job.ID,
		"retry_count", job.RetryCount,
		"error", errorMsg)

	// Increment retry count
	if err := e.treasuryRepo.IncrementJobRetryCount(ctx, job.ID); err != nil {
		return err
	}

	// Reset to pending status
	updateReq := &entities.UpdateConversionJobStatusRequest{
		JobID:        job.ID,
		NewStatus:    entities.ConversionJobStatusPending,
		ErrorMessage: &errorMsg,
	}

	return e.treasuryRepo.UpdateConversionJobStatus(ctx, updateReq)
}

func stringPtr(s string) *string {
	return &s
}
