package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ConversionRepository handles conversion job persistence
type ConversionRepository struct {
	db *sqlx.DB
}

// NewConversionRepository creates a new conversion repository
func NewConversionRepository(db *sqlx.DB) *ConversionRepository {
	return &ConversionRepository{db: db}
}

// GetByID retrieves a conversion job by ID
func (r *ConversionRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.ConversionJob, error) {
	query := `
		SELECT id, direction, amount, status, trigger_reason,
		       provider_id, provider_name, provider_tx_id, provider_response,
		       ledger_transaction_id,
		       source_account_id, destination_account_id,
		       source_amount, destination_amount, exchange_rate, fees_paid,
		       scheduled_at, submitted_at, provider_completed_at, completed_at, failed_at,
		       error_message, error_code, retry_count, max_retries,
		       idempotency_key, notes,
		       created_at, updated_at
		FROM conversion_jobs
		WHERE id = $1
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

// GetCompletedJobsWithoutLedgerEntries returns conversion jobs that are completed but don't have ledger entries
// Used by reconciliation service to verify all conversions are recorded
func (r *ConversionRepository) GetCompletedJobsWithoutLedgerEntries(ctx context.Context) ([]*entities.ConversionJob, error) {
	query := `
		SELECT id, direction, amount, status, trigger_reason,
		       provider_id, provider_name, provider_tx_id, provider_response,
		       ledger_transaction_id,
		       source_account_id, destination_account_id,
		       source_amount, destination_amount, exchange_rate, fees_paid,
		       scheduled_at, submitted_at, provider_completed_at, completed_at, failed_at,
		       error_message, error_code, retry_count, max_retries,
		       idempotency_key, notes,
		       created_at, updated_at
		FROM conversion_jobs
		WHERE status = $1 
		  AND ledger_transaction_id IS NULL
		ORDER BY completed_at ASC
	`

	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, entities.ConversionJobStatusCompleted)
	if err != nil {
		if err == sql.ErrNoRows {
			return []*entities.ConversionJob{}, nil
		}
		return nil, fmt.Errorf("failed to get completed jobs without ledger entries: %w", err)
	}

	return jobs, nil
}

// GetPendingJobs retrieves all pending conversion jobs
func (r *ConversionRepository) GetPendingJobs(ctx context.Context) ([]*entities.ConversionJob, error) {
	query := `
		SELECT id, direction, amount, status, trigger_reason,
		       provider_id, provider_name, provider_tx_id, provider_response,
		       ledger_transaction_id,
		       source_account_id, destination_account_id,
		       source_amount, destination_amount, exchange_rate, fees_paid,
		       scheduled_at, submitted_at, provider_completed_at, completed_at, failed_at,
		       error_message, error_code, retry_count, max_retries,
		       idempotency_key, notes,
		       created_at, updated_at
		FROM conversion_jobs
		WHERE status = $1
		ORDER BY created_at ASC
	`

	var jobs []*entities.ConversionJob
	err := r.db.SelectContext(ctx, &jobs, query, entities.ConversionJobStatusPending)
	if err != nil {
		if err == sql.ErrNoRows {
			return []*entities.ConversionJob{}, nil
		}
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}

	return jobs, nil
}

// Create creates a new conversion job
func (r *ConversionRepository) Create(ctx context.Context, job *entities.ConversionJob) error {
	query := `
		INSERT INTO conversion_jobs (
			id, direction, amount, status, trigger_reason,
			provider_id, provider_name, provider_tx_id, provider_response,
			ledger_transaction_id,
			source_account_id, destination_account_id,
			source_amount, destination_amount, exchange_rate, fees_paid,
			scheduled_at, submitted_at, provider_completed_at, completed_at, failed_at,
			error_message, error_code, retry_count, max_retries,
			idempotency_key, notes,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10,
			$11, $12,
			$13, $14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23, $24, $25,
			$26, $27,
			$28, $29
		)
	`

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.Direction, job.Amount, job.Status, job.TriggerReason,
		job.ProviderID, job.ProviderName, job.ProviderTxID, job.ProviderResponse,
		job.LedgerTransactionID,
		job.SourceAccountID, job.DestinationAccountID,
		job.SourceAmount, job.DestinationAmount, job.ExchangeRate, job.FeesPaid,
		job.ScheduledAt, job.SubmittedAt, job.ProviderCompletedAt, job.CompletedAt, job.FailedAt,
		job.ErrorMessage, job.ErrorCode, job.RetryCount, job.MaxRetries,
		job.IdempotencyKey, job.Notes,
		job.CreatedAt, job.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create conversion job: %w", err)
	}

	return nil
}

// UpdateStatus updates the conversion job status
func (r *ConversionRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.ConversionJobStatus) error {
	query := `
		UPDATE conversion_jobs
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`

	_, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update conversion job status: %w", err)
	}

	return nil
}

// UpdateLedgerTransaction updates the ledger transaction ID for a conversion job
func (r *ConversionRepository) UpdateLedgerTransaction(ctx context.Context, id, ledgerTxID uuid.UUID) error {
	query := `
		UPDATE conversion_jobs
		SET ledger_transaction_id = $1, updated_at = NOW()
		WHERE id = $2
	`

	_, err := r.db.ExecContext(ctx, query, ledgerTxID, id)
	if err != nil {
		return fmt.Errorf("failed to update ledger transaction: %w", err)
	}

	return nil
}
