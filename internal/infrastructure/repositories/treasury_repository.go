package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// TreasuryRepository handles treasury-related database operations
type TreasuryRepository struct {
	db *sqlx.DB
}

// NewTreasuryRepository creates a new treasury repository
func NewTreasuryRepository(db *sqlx.DB) *TreasuryRepository {
	return &TreasuryRepository{db: db}
}

// ============================================================================
// CONVERSION PROVIDER OPERATIONS
// ============================================================================

// CreateProvider creates a new conversion provider
func (r *TreasuryRepository) CreateProvider(ctx context.Context, provider *entities.ConversionProvider) error {
	query := `
		INSERT INTO conversion_providers (
			id, name, provider_type, priority, status,
			supports_usdc_to_usd, supports_usd_to_usdc,
			min_conversion_amount, max_conversion_amount,
			daily_volume_limit, daily_volume_used,
			success_count, failure_count,
			api_credentials_encrypted, webhook_secret, notes,
			created_at, updated_at
		) VALUES (
			:id, :name, :provider_type, :priority, :status,
			:supports_usdc_to_usd, :supports_usd_to_usdc,
			:min_conversion_amount, :max_conversion_amount,
			:daily_volume_limit, :daily_volume_used,
			:success_count, :failure_count,
			:api_credentials_encrypted, :webhook_secret, :notes,
			:created_at, :updated_at
		)
	`
	_, err := r.db.NamedExecContext(ctx, query, provider)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code == "23505" { // unique_violation
				return fmt.Errorf("provider with this name already exists: %w", err)
			}
		}
		return fmt.Errorf("failed to create provider: %w", err)
	}
	return nil
}

// GetProviderByID retrieves a provider by ID
func (r *TreasuryRepository) GetProviderByID(ctx context.Context, id uuid.UUID) (*entities.ConversionProvider, error) {
	query := `
		SELECT * FROM conversion_providers WHERE id = $1
	`
	var provider entities.ConversionProvider
	err := r.db.GetContext(ctx, &provider, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider not found")
		}
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	return &provider, nil
}

// GetProviderByName retrieves a provider by name
func (r *TreasuryRepository) GetProviderByName(ctx context.Context, name string) (*entities.ConversionProvider, error) {
	query := `
		SELECT * FROM conversion_providers WHERE name = $1
	`
	var provider entities.ConversionProvider
	err := r.db.GetContext(ctx, &provider, query, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider not found")
		}
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	return &provider, nil
}

// ListProviders retrieves all providers ordered by priority
func (r *TreasuryRepository) ListProviders(ctx context.Context, activeOnly bool) ([]*entities.ConversionProvider, error) {
	query := `
		SELECT * FROM conversion_providers
		WHERE ($1 = false OR status = 'active')
		ORDER BY priority ASC
	`
	var providers []*entities.ConversionProvider
	err := r.db.SelectContext(ctx, &providers, query, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("failed to list providers: %w", err)
	}
	return providers, nil
}

// UpdateProvider updates an existing provider
func (r *TreasuryRepository) UpdateProvider(ctx context.Context, provider *entities.ConversionProvider) error {
	query := `
		UPDATE conversion_providers SET
			name = :name,
			provider_type = :provider_type,
			priority = :priority,
			status = :status,
			supports_usdc_to_usd = :supports_usdc_to_usd,
			supports_usd_to_usdc = :supports_usd_to_usdc,
			min_conversion_amount = :min_conversion_amount,
			max_conversion_amount = :max_conversion_amount,
			daily_volume_limit = :daily_volume_limit,
			daily_volume_used = :daily_volume_used,
			success_count = :success_count,
			failure_count = :failure_count,
			last_success_at = :last_success_at,
			last_failure_at = :last_failure_at,
			api_credentials_encrypted = :api_credentials_encrypted,
			webhook_secret = :webhook_secret,
			notes = :notes
		WHERE id = :id
	`
	result, err := r.db.NamedExecContext(ctx, query, provider)
	if err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider not found")
	}
	return nil
}

// UpdateProviderHealth updates provider health metrics
func (r *TreasuryRepository) UpdateProviderHealth(ctx context.Context, providerID uuid.UUID, success bool) error {
	query := `
		UPDATE conversion_providers SET
			success_count = CASE WHEN $2 THEN success_count + 1 ELSE success_count END,
			failure_count = CASE WHEN $2 THEN failure_count ELSE failure_count + 1 END,
			last_success_at = CASE WHEN $2 THEN NOW() ELSE last_success_at END,
			last_failure_at = CASE WHEN $2 THEN last_failure_at ELSE NOW() END
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, providerID, success)
	if err != nil {
		return fmt.Errorf("failed to update provider health: %w", err)
	}
	return nil
}

// ResetProviderDailyVolume resets the daily volume counter for a provider
func (r *TreasuryRepository) ResetProviderDailyVolume(ctx context.Context, providerID uuid.UUID) error {
	query := `
		UPDATE conversion_providers SET daily_volume_used = 0 WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, providerID)
	if err != nil {
		return fmt.Errorf("failed to reset provider daily volume: %w", err)
	}
	return nil
}

// IncrementProviderVolume adds to the provider's daily volume
func (r *TreasuryRepository) IncrementProviderVolume(ctx context.Context, providerID uuid.UUID, amount decimal.Decimal) error {
	query := `
		UPDATE conversion_providers SET daily_volume_used = daily_volume_used + $2 WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, providerID, amount)
	if err != nil {
		return fmt.Errorf("failed to increment provider volume: %w", err)
	}
	return nil
}

// ============================================================================
// BUFFER THRESHOLD OPERATIONS
// ============================================================================

// CreateBufferThreshold creates a new buffer threshold configuration
func (r *TreasuryRepository) CreateBufferThreshold(ctx context.Context, threshold *entities.BufferThreshold) error {
	query := `
		INSERT INTO buffer_thresholds (
			id, account_type, min_threshold, target_threshold, max_threshold,
			conversion_batch_size, notes, created_at, updated_at
		) VALUES (
			:id, :account_type, :min_threshold, :target_threshold, :max_threshold,
			:conversion_batch_size, :notes, :created_at, :updated_at
		)
	`
	_, err := r.db.NamedExecContext(ctx, query, threshold)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code == "23505" { // unique_violation
				return fmt.Errorf("threshold for this account type already exists: %w", err)
			}
		}
		return fmt.Errorf("failed to create buffer threshold: %w", err)
	}
	return nil
}

// GetBufferThresholdByAccountType retrieves threshold config by account type
func (r *TreasuryRepository) GetBufferThresholdByAccountType(ctx context.Context, accountType entities.AccountType) (*entities.BufferThreshold, error) {
	query := `
		SELECT * FROM buffer_thresholds WHERE account_type = $1
	`
	var threshold entities.BufferThreshold
	err := r.db.GetContext(ctx, &threshold, query, accountType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("threshold not found for account type")
		}
		return nil, fmt.Errorf("failed to get buffer threshold: %w", err)
	}
	return &threshold, nil
}

// ListBufferThresholds retrieves all buffer thresholds
func (r *TreasuryRepository) ListBufferThresholds(ctx context.Context) ([]*entities.BufferThreshold, error) {
	query := `
		SELECT * FROM buffer_thresholds ORDER BY account_type
	`
	var thresholds []*entities.BufferThreshold
	err := r.db.SelectContext(ctx, &thresholds, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list buffer thresholds: %w", err)
	}
	return thresholds, nil
}

// UpdateBufferThreshold updates an existing buffer threshold
func (r *TreasuryRepository) UpdateBufferThreshold(ctx context.Context, threshold *entities.BufferThreshold) error {
	query := `
		UPDATE buffer_thresholds SET
			min_threshold = :min_threshold,
			target_threshold = :target_threshold,
			max_threshold = :max_threshold,
			conversion_batch_size = :conversion_batch_size,
			notes = :notes
		WHERE id = :id
	`
	result, err := r.db.NamedExecContext(ctx, query, threshold)
	if err != nil {
		return fmt.Errorf("failed to update buffer threshold: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("buffer threshold not found")
	}
	return nil
}

// GetBufferStatus retrieves current buffer status with health check
func (r *TreasuryRepository) GetBufferStatus(ctx context.Context, accountType entities.AccountType) (*entities.BufferStatus, error) {
	query := `
		SELECT 
			bt.account_type,
			bt.min_threshold,
			bt.target_threshold,
			bt.max_threshold,
			la.balance as current_balance,
			CASE
				WHEN la.balance < bt.min_threshold THEN 'CRITICAL_LOW'
				WHEN la.balance < bt.target_threshold THEN 'BELOW_TARGET'
				WHEN la.balance > bt.max_threshold THEN 'OVER_CAPITALIZED'
				ELSE 'HEALTHY'
			END as status,
			(bt.target_threshold - la.balance) as amount_to_target
		FROM buffer_thresholds bt
		JOIN ledger_accounts la ON la.account_type = bt.account_type
		WHERE bt.account_type = $1
	`
	var status entities.BufferStatus
	err := r.db.GetContext(ctx, &status, query, accountType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("buffer status not found")
		}
		return nil, fmt.Errorf("failed to get buffer status: %w", err)
	}
	return &status, nil
}

// ListAllBufferStatuses retrieves all buffer statuses ordered by health priority
func (r *TreasuryRepository) ListAllBufferStatuses(ctx context.Context) ([]*entities.BufferStatus, error) {
	query := `
		SELECT * FROM v_buffer_status
	`
	var statuses []*entities.BufferStatus
	err := r.db.SelectContext(ctx, &statuses, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list buffer statuses: %w", err)
	}
	return statuses, nil
}

// ============================================================================
// CONVERSION JOB OPERATIONS
// ============================================================================



// GetConversionJobByID retrieves a conversion job by ID
func (r *TreasuryRepository) GetConversionJobByID(ctx context.Context, id uuid.UUID) (*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs WHERE id = $1
	`
	var job entities.ConversionJob
	err := r.db.GetContext(ctx, &job, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("conversion job not found")
		}
		return nil, fmt.Errorf("failed to get conversion job: %w", err)
	}
	return &job, nil
}

// GetConversionJobByIdempotencyKey retrieves a job by idempotency key
func (r *TreasuryRepository) GetConversionJobByIdempotencyKey(ctx context.Context, key string) (*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs WHERE idempotency_key = $1
	`
	var job entities.ConversionJob
	err := r.db.GetContext(ctx, &job, query, key)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("conversion job not found")
		}
		return nil, fmt.Errorf("failed to get conversion job: %w", err)
	}
	return &job, nil
}

// GetConversionJobByProviderTxID retrieves a job by provider transaction ID
func (r *TreasuryRepository) GetConversionJobByProviderTxID(ctx context.Context, providerTxID string) (*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs WHERE provider_tx_id = $1
	`
	var job entities.ConversionJob
	err := r.db.GetContext(ctx, &job, query, providerTxID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("conversion job not found")
		}
		return nil, fmt.Errorf("failed to get conversion job: %w", err)
	}
	return &job, nil
}

// ListPendingConversionJobs retrieves all pending jobs ready for execution
func (r *TreasuryRepository) ListPendingConversionJobs(ctx context.Context, limit int) ([]*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs
		WHERE status = 'pending'
		  AND (scheduled_at IS NULL OR scheduled_at <= NOW())
		ORDER BY scheduled_at ASC NULLS FIRST, created_at ASC
		LIMIT $1
	`
	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending conversion jobs: %w", err)
	}
	return jobs, nil
}

// ListActiveConversionJobs retrieves all active (non-final) jobs
func (r *TreasuryRepository) ListActiveConversionJobs(ctx context.Context) ([]*entities.ConversionJob, error) {
	query := `
		SELECT * FROM v_active_conversion_jobs
	`
	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list active conversion jobs: %w", err)
	}
	return jobs, nil
}

// ListConversionJobsByStatus retrieves jobs by status with pagination
func (r *TreasuryRepository) ListConversionJobsByStatus(ctx context.Context, status entities.ConversionJobStatus, limit, offset int) ([]*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversion jobs: %w", err)
	}
	return jobs, nil
}

// UpdateConversionJob updates an existing conversion job
func (r *TreasuryRepository) UpdateConversionJob(ctx context.Context, job *entities.ConversionJob) error {
	query := `
		UPDATE conversion_jobs SET
			status = :status,
			provider_id = :provider_id,
			provider_name = :provider_name,
			provider_tx_id = :provider_tx_id,
			provider_response = :provider_response,
			ledger_transaction_id = :ledger_transaction_id,
			source_amount = :source_amount,
			destination_amount = :destination_amount,
			exchange_rate = :exchange_rate,
			fees_paid = :fees_paid,
			submitted_at = :submitted_at,
			provider_completed_at = :provider_completed_at,
			completed_at = :completed_at,
			failed_at = :failed_at,
			error_message = :error_message,
			error_code = :error_code,
			retry_count = :retry_count,
			notes = :notes
		WHERE id = :id
	`
	result, err := r.db.NamedExecContext(ctx, query, job)
	if err != nil {
		return fmt.Errorf("failed to update conversion job: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("conversion job not found")
	}
	return nil
}



// GetConversionJobHistory retrieves the audit history for a job
func (r *TreasuryRepository) GetConversionJobHistory(ctx context.Context, jobID uuid.UUID) ([]*entities.ConversionJobHistory, error) {
	query := `
		SELECT * FROM conversion_job_history
		WHERE conversion_job_id = $1
		ORDER BY created_at ASC
	`
	var history []*entities.ConversionJobHistory
	err := r.db.SelectContext(ctx, &history, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversion job history: %w", err)
	}
	return history, nil
}

// GetActiveProviders retrieves all active providers ordered by priority
func (r *TreasuryRepository) GetActiveProviders(ctx context.Context) ([]*entities.ConversionProvider, error) {
	return r.ListProviders(ctx, true)
}

// GetJobsByStatus retrieves jobs by a specific status without pagination
func (r *TreasuryRepository) GetJobsByStatus(ctx context.Context, status entities.ConversionJobStatus) ([]*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs
		WHERE status = $1
		ORDER BY created_at ASC
	`
	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, status)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs by status: %w", err)
	}
	return jobs, nil
}

// GetStaleJobs retrieves jobs that have been in processing state for too long
func (r *TreasuryRepository) GetStaleJobs(ctx context.Context, staleThreshold time.Time) ([]*entities.ConversionJob, error) {
	query := `
		SELECT * FROM conversion_jobs
		WHERE status IN ('provider_submitted', 'provider_processing')
		  AND submitted_at IS NOT NULL
		  AND submitted_at < $1
		ORDER BY submitted_at ASC
	`
	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, staleThreshold)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale jobs: %w", err)
	}
	return jobs, nil
}

// IncrementJobRetryCount increments the retry counter for a job
func (r *TreasuryRepository) IncrementJobRetryCount(ctx context.Context, jobID uuid.UUID) error {
	query := `
		UPDATE conversion_jobs
		SET retry_count = retry_count + 1
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, jobID)
	if err != nil {
		return fmt.Errorf("failed to increment retry count: %w", err)
	}
	return nil
}

// UpdateJobLedgerTransaction links a ledger transaction to a conversion job
func (r *TreasuryRepository) UpdateJobLedgerTransaction(ctx context.Context, jobID, ledgerTxID uuid.UUID) error {
	query := `
		UPDATE conversion_jobs
		SET ledger_transaction_id = $2
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, jobID, ledgerTxID)
	if err != nil {
		return fmt.Errorf("failed to update job ledger transaction: %w", err)
	}
	return nil
}

// UpdateConversionJobStatus updates job status and related fields
func (r *TreasuryRepository) UpdateConversionJobStatus(ctx context.Context, req *entities.UpdateConversionJobStatusRequest) error {
	// Build dynamic update query based on provided fields
	query := `
		UPDATE conversion_jobs SET
			status = $2,
			updated_at = NOW()
	`
	args := []interface{}{req.JobID, req.NewStatus}
	argCount := 2

	// Add optional fields
	if req.ProviderTxID != nil {
		argCount++
		query += fmt.Sprintf(", provider_tx_id = $%d", argCount)
		args = append(args, *req.ProviderTxID)
	}
	if req.ProviderResponse != nil {
		argCount++
		query += fmt.Sprintf(", provider_response = $%d", argCount)
		args = append(args, *req.ProviderResponse)
	}
	if req.SourceAmount != nil {
		argCount++
		query += fmt.Sprintf(", source_amount = $%d", argCount)
		args = append(args, *req.SourceAmount)
	}
	if req.DestinationAmount != nil {
		argCount++
		query += fmt.Sprintf(", destination_amount = $%d", argCount)
		args = append(args, *req.DestinationAmount)
	}
	if req.ExchangeRate != nil {
		argCount++
		query += fmt.Sprintf(", exchange_rate = $%d", argCount)
		args = append(args, *req.ExchangeRate)
	}
	if req.FeesPaid != nil {
		argCount++
		query += fmt.Sprintf(", fees_paid = $%d", argCount)
		args = append(args, *req.FeesPaid)
	}
	if req.ErrorMessage != nil {
		argCount++
		query += fmt.Sprintf(", error_message = $%d", argCount)
		args = append(args, *req.ErrorMessage)
	}
	if req.ErrorCode != nil {
		argCount++
		query += fmt.Sprintf(", error_code = $%d", argCount)
		args = append(args, *req.ErrorCode)
	}

	// Set timestamps based on status
	switch req.NewStatus {
	case entities.ConversionJobStatusProviderSubmitted:
		query += ", submitted_at = NOW()"
	case entities.ConversionJobStatusProviderCompleted:
		query += ", provider_completed_at = NOW()"
	case entities.ConversionJobStatusCompleted:
		query += ", completed_at = NOW()"
	case entities.ConversionJobStatusFailed:
		query += ", failed_at = NOW()"
	}

	query += " WHERE id = $1"

	_, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update conversion job status: %w", err)
	}
	return nil
}

// CreateConversionJob creates a new conversion job from request
func (r *TreasuryRepository) CreateConversionJob(ctx context.Context, req *entities.CreateConversionJobRequest) (*entities.ConversionJob, error) {
	now := time.Now()
	job := &entities.ConversionJob{
		ID:                   uuid.New(),
		Direction:            req.Direction,
		Amount:               req.Amount,
		Status:               entities.ConversionJobStatusPending,
		TriggerReason:        req.TriggerReason,
		SourceAccountID:      &req.SourceAccountID,
		DestinationAccountID: &req.DestinationAccountID,
		ScheduledAt:          req.ScheduledAt,
		IdempotencyKey:       &req.IdempotencyKey,
		Notes:                req.Notes,
		MaxRetries:           3,
		RetryCount:           0,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	query := `
		INSERT INTO conversion_jobs (
			id, direction, amount, status, trigger_reason,
			source_account_id, destination_account_id,
			scheduled_at, idempotency_key, notes,
			retry_count, max_retries,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, $10,
			$11, $12,
			$13, $14
		)
	`
	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.Direction, job.Amount, job.Status, job.TriggerReason,
		job.SourceAccountID, job.DestinationAccountID,
		job.ScheduledAt, job.IdempotencyKey, job.Notes,
		job.RetryCount, job.MaxRetries,
		job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code == "23505" { // unique_violation
				return nil, fmt.Errorf("job with this idempotency key already exists: %w", err)
			}
		}
		return nil, fmt.Errorf("failed to create conversion job: %w", err)
	}

	return job, nil
}

// GetAllBufferThresholds retrieves all buffer threshold configurations
func (r *TreasuryRepository) GetAllBufferThresholds(ctx context.Context) ([]*entities.BufferThreshold, error) {
	return r.ListBufferThresholds(ctx)
}

// ============================================================================
// ANALYTICS & REPORTING
// ============================================================================

// GetConversionStats retrieves conversion statistics for a time period
func (r *TreasuryRepository) GetConversionStats(ctx context.Context, since, until *string) (map[string]interface{}, error) {
	query := `
		SELECT 
			COUNT(*) as total_jobs,
			COUNT(*) FILTER (WHERE status = 'completed') as completed_jobs,
			COUNT(*) FILTER (WHERE status = 'failed') as failed_jobs,
			SUM(source_amount) FILTER (WHERE status = 'completed') as total_converted,
			SUM(fees_paid) FILTER (WHERE status = 'completed') as total_fees,
			AVG(EXTRACT(EPOCH FROM (completed_at - created_at))) FILTER (WHERE status = 'completed') as avg_completion_time_seconds
		FROM conversion_jobs
		WHERE ($1::timestamp IS NULL OR created_at >= $1::timestamp)
		  AND ($2::timestamp IS NULL OR created_at <= $2::timestamp)
	`
	var stats struct {
		TotalJobs               int              `db:"total_jobs"`
		CompletedJobs           int              `db:"completed_jobs"`
		FailedJobs              int              `db:"failed_jobs"`
		TotalConverted          *decimal.Decimal `db:"total_converted"`
		TotalFees               *decimal.Decimal `db:"total_fees"`
		AvgCompletionTimeSeconds *float64         `db:"avg_completion_time_seconds"`
	}
	
	err := r.db.GetContext(ctx, &stats, query, since, until)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversion stats: %w", err)
	}

	result := map[string]interface{}{
		"total_jobs":       stats.TotalJobs,
		"completed_jobs":   stats.CompletedJobs,
		"failed_jobs":      stats.FailedJobs,
		"total_converted":  stats.TotalConverted,
		"total_fees":       stats.TotalFees,
		"avg_completion_time_seconds": stats.AvgCompletionTimeSeconds,
	}
	
	return result, nil
}
