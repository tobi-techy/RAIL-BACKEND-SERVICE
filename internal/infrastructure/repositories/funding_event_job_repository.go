package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	// "github.com/lib/pq"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// FundingEventJobRepository manages funding event job persistence
type FundingEventJobRepository struct {
	db     *sql.DB
	logger *logger.Logger
}

// NewFundingEventJobRepository creates a new funding event job repository
func NewFundingEventJobRepository(db *sql.DB, logger *logger.Logger) *FundingEventJobRepository {
	return &FundingEventJobRepository{
		db:     db,
		logger: logger,
	}
}

// Enqueue creates a new job or returns existing job if duplicate (idempotent)
func (r *FundingEventJobRepository) Enqueue(ctx context.Context, job *entities.FundingEventJob) error {
	webhookJSON, err := json.Marshal(job.WebhookPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	logsJSON, err := json.Marshal(job.ProcessingLogs)
	if err != nil {
		return fmt.Errorf("failed to marshal processing logs: %w", err)
	}

	query := `
		INSERT INTO funding_event_jobs (
			id, tx_hash, chain, token, amount, to_address, status,
			attempt_count, max_attempts, first_seen_at, webhook_payload,
			processing_logs, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)
		ON CONFLICT (tx_hash, chain) DO NOTHING
		RETURNING id`

	err = r.db.QueryRowContext(ctx, query,
		job.ID,
		job.TxHash,
		string(job.Chain),
		string(job.Token),
		job.Amount,
		job.ToAddress,
		string(job.Status),
		job.AttemptCount,
		job.MaxAttempts,
		job.FirstSeenAt,
		webhookJSON,
		logsJSON,
		job.CreatedAt,
		job.UpdatedAt,
	).Scan(&job.ID)

	if err == sql.ErrNoRows {
		// Job already exists, this is ok (idempotency)
		r.logger.Debug("Job already exists, skipping", "tx_hash", job.TxHash, "chain", job.Chain)
		return nil
	}

	if err != nil {
		r.logger.Error("Failed to enqueue job", "error", err, "tx_hash", job.TxHash)
		return fmt.Errorf("failed to enqueue job: %w", err)
	}

	r.logger.Info("Job enqueued successfully", "job_id", job.ID, "tx_hash", job.TxHash)
	return nil
}

// GetNextPendingJobs retrieves jobs ready for processing
func (r *FundingEventJobRepository) GetNextPendingJobs(ctx context.Context, limit int) ([]*entities.FundingEventJob, error) {
	query := `
		SELECT 
			id, tx_hash, chain, token, amount, to_address, status,
			attempt_count, max_attempts, last_error, error_type, failure_reason,
			first_seen_at, last_attempt_at, next_retry_at, completed_at, moved_to_dlq_at,
			webhook_payload, processing_logs, created_at, updated_at
		FROM funding_event_jobs
		WHERE status = 'pending' 
		   OR (status = 'failed' AND next_retry_at IS NOT NULL AND next_retry_at <= NOW())
		ORDER BY first_seen_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		r.logger.Error("Failed to get next pending jobs", "error", err)
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*entities.FundingEventJob
	for rows.Next() {
		job, err := r.scanJob(rows)
		if err != nil {
			r.logger.Error("Failed to scan job", "error", err)
			continue
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// Update updates a job's status and metadata
func (r *FundingEventJobRepository) Update(ctx context.Context, job *entities.FundingEventJob) error {
	logsJSON, err := json.Marshal(job.ProcessingLogs)
	if err != nil {
		return fmt.Errorf("failed to marshal processing logs: %w", err)
	}

	query := `
		UPDATE funding_event_jobs
		SET 
			status = $1,
			attempt_count = $2,
			last_error = $3,
			error_type = $4,
			failure_reason = $5,
			last_attempt_at = $6,
			next_retry_at = $7,
			completed_at = $8,
			moved_to_dlq_at = $9,
			processing_logs = $10,
			updated_at = $11
		WHERE id = $12`

	var errorType *string
	if job.ErrorType != nil {
		et := string(*job.ErrorType)
		errorType = &et
	}

	result, err := r.db.ExecContext(ctx, query,
		string(job.Status),
		job.AttemptCount,
		job.LastError,
		errorType,
		job.FailureReason,
		job.LastAttemptAt,
		job.NextRetryAt,
		job.CompletedAt,
		job.MovedToDLQAt,
		logsJSON,
		job.UpdatedAt,
		job.ID,
	)

	if err != nil {
		r.logger.Error("Failed to update job", "error", err, "job_id", job.ID)
		return fmt.Errorf("failed to update job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("job not found: %s", job.ID)
	}

	return nil
}

// GetByID retrieves a job by ID
func (r *FundingEventJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.FundingEventJob, error) {
	query := `
		SELECT 
			id, tx_hash, chain, token, amount, to_address, status,
			attempt_count, max_attempts, last_error, error_type, failure_reason,
			first_seen_at, last_attempt_at, next_retry_at, completed_at, moved_to_dlq_at,
			webhook_payload, processing_logs, created_at, updated_at
		FROM funding_event_jobs
		WHERE id = $1`

	row := r.db.QueryRowContext(ctx, query, id)
	job, err := r.scanJob(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		r.logger.Error("Failed to get job by ID", "error", err, "job_id", id)
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	return job, nil
}

// GetByTxHash retrieves a job by transaction hash and chain
func (r *FundingEventJobRepository) GetByTxHash(ctx context.Context, txHash string, chain entities.Chain) (*entities.FundingEventJob, error) {
	query := `
		SELECT 
			id, tx_hash, chain, token, amount, to_address, status,
			attempt_count, max_attempts, last_error, error_type, failure_reason,
			first_seen_at, last_attempt_at, next_retry_at, completed_at, moved_to_dlq_at,
			webhook_payload, processing_logs, created_at, updated_at
		FROM funding_event_jobs
		WHERE tx_hash = $1 AND chain = $2`

	row := r.db.QueryRowContext(ctx, query, txHash, string(chain))
	job, err := r.scanJob(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		r.logger.Error("Failed to get job by tx hash", "error", err, "tx_hash", txHash)
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	return job, nil
}

// GetDLQJobs retrieves jobs in the dead letter queue
func (r *FundingEventJobRepository) GetDLQJobs(ctx context.Context, limit int, offset int) ([]*entities.FundingEventJob, error) {
	query := `
		SELECT 
			id, tx_hash, chain, token, amount, to_address, status,
			attempt_count, max_attempts, last_error, error_type, failure_reason,
			first_seen_at, last_attempt_at, next_retry_at, completed_at, moved_to_dlq_at,
			webhook_payload, processing_logs, created_at, updated_at
		FROM funding_event_jobs
		WHERE status = 'dlq'
		ORDER BY moved_to_dlq_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		r.logger.Error("Failed to get DLQ jobs", "error", err)
		return nil, fmt.Errorf("failed to get DLQ jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*entities.FundingEventJob
	for rows.Next() {
		job, err := r.scanJob(rows)
		if err != nil {
			r.logger.Error("Failed to scan DLQ job", "error", err)
			continue
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// GetPendingDepositsForReconciliation retrieves deposits stuck in pending state
func (r *FundingEventJobRepository) GetPendingDepositsForReconciliation(ctx context.Context, threshold time.Duration, limit int) ([]*entities.ReconciliationCandidate, error) {
	thresholdTime := time.Now().Add(-threshold)

	query := `
		SELECT 
			d.id as deposit_id,
			d.user_id,
			d.tx_hash,
			d.chain,
			d.token,
			d.amount,
			w.address as to_address,
			d.status,
			d.created_at,
			EXTRACT(EPOCH FROM (NOW() - d.created_at))::BIGINT as pending_duration_seconds
		FROM deposits d
		LEFT JOIN wallets w ON d.user_id = w.user_id AND d.chain = w.chain
		WHERE d.status = 'pending'
		  AND d.created_at < $1
		ORDER BY d.created_at ASC
		LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, thresholdTime, limit)
	if err != nil {
		r.logger.Error("Failed to get reconciliation candidates", "error", err)
		return nil, fmt.Errorf("failed to get reconciliation candidates: %w", err)
	}
	defer rows.Close()

	var candidates []*entities.ReconciliationCandidate
	for rows.Next() {
		var c entities.ReconciliationCandidate
		var durationSeconds int64

		err := rows.Scan(
			&c.DepositID,
			&c.UserID,
			&c.TxHash,
			&c.Chain,
			&c.Token,
			&c.Amount,
			&c.ToAddress,
			&c.Status,
			&c.CreatedAt,
			&durationSeconds,
		)
		if err != nil {
			r.logger.Error("Failed to scan reconciliation candidate", "error", err)
			continue
		}

		c.PendingDuration = time.Duration(durationSeconds) * time.Second
		candidates = append(candidates, &c)
	}

	return candidates, nil
}

// GetMetrics retrieves webhook processing metrics
func (r *FundingEventJobRepository) GetMetrics(ctx context.Context) (*entities.WebhookMetrics, error) {
	query := `
		SELECT 
			COUNT(*) as total_received,
			COUNT(*) FILTER (WHERE status = 'completed') as total_processed,
			COUNT(*) FILTER (WHERE status = 'failed') as total_failed,
			COUNT(*) FILTER (WHERE status = 'dlq') as total_dlq,
			COUNT(*) FILTER (WHERE status = 'pending') as pending_count,
			COALESCE(AVG(attempt_count) FILTER (WHERE status IN ('completed', 'dlq')), 0) as avg_retry_count,
			COALESCE(AVG(EXTRACT(EPOCH FROM (completed_at - first_seen_at))) FILTER (WHERE completed_at IS NOT NULL), 0) as avg_latency_seconds
		FROM funding_event_jobs
		WHERE created_at > NOW() - INTERVAL '24 hours'`

	var metrics entities.WebhookMetrics
	var avgLatencySeconds float64

	err := r.db.QueryRowContext(ctx, query).Scan(
		&metrics.TotalReceived,
		&metrics.TotalProcessed,
		&metrics.TotalFailed,
		&metrics.TotalDLQ,
		&metrics.PendingCount,
		&metrics.AverageRetryCount,
		&avgLatencySeconds,
	)

	if err != nil {
		r.logger.Error("Failed to get metrics", "error", err)
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	metrics.AverageLatency = time.Duration(avgLatencySeconds * float64(time.Second))
	metrics.DLQDepth = metrics.TotalDLQ
	metrics.CalculateSuccessRate()

	return &metrics, nil
}

// scanJob is a helper to scan a job from a row
func (r *FundingEventJobRepository) scanJob(scanner interface {
	Scan(dest ...interface{}) error
}) (*entities.FundingEventJob, error) {
	var job entities.FundingEventJob
	var chain, token, status string
	var errorType *string
	var webhookJSON, logsJSON []byte

	err := scanner.Scan(
		&job.ID,
		&job.TxHash,
		&chain,
		&token,
		&job.Amount,
		&job.ToAddress,
		&status,
		&job.AttemptCount,
		&job.MaxAttempts,
		&job.LastError,
		&errorType,
		&job.FailureReason,
		&job.FirstSeenAt,
		&job.LastAttemptAt,
		&job.NextRetryAt,
		&job.CompletedAt,
		&job.MovedToDLQAt,
		&webhookJSON,
		&logsJSON,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	job.Chain = entities.Chain(chain)
	job.Token = entities.Stablecoin(token)
	job.Status = entities.FundingEventJobStatus(status)

	if errorType != nil {
		et := entities.FundingEventErrorType(*errorType)
		job.ErrorType = &et
	}

	// Unmarshal JSON fields
	if len(webhookJSON) > 0 {
		if err := json.Unmarshal(webhookJSON, &job.WebhookPayload); err != nil {
			r.logger.Warn("Failed to unmarshal webhook payload", "error", err)
		}
	}

	if len(logsJSON) > 0 {
		if err := json.Unmarshal(logsJSON, &job.ProcessingLogs); err != nil {
			r.logger.Warn("Failed to unmarshal processing logs", "error", err)
		}
	}

	return &job, nil
}
