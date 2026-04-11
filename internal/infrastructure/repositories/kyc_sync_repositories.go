package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// SumsubWebhookEventRepository stores deduplicated webhook events.
type SumsubWebhookEventRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewSumsubWebhookEventRepository(db *sql.DB, logger *zap.Logger) *SumsubWebhookEventRepository {
	return &SumsubWebhookEventRepository{db: db, logger: logger}
}

// CreateIfNotExists inserts a webhook event if dedupe key is new.
func (r *SumsubWebhookEventRepository) CreateIfNotExists(ctx context.Context, event *entities.SumsubWebhookEvent) (bool, error) {
	const query = `
		INSERT INTO sumsub_webhook_events (
			id, dedupe_key, applicant_id, correlation_id, event_type, payload, received_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`

	err := r.db.QueryRowContext(ctx, query,
		event.ID,
		event.DedupeKey,
		event.ApplicantID,
		nullIfEmpty(event.CorrelationID),
		event.EventType,
		event.Payload,
		event.ReceivedAt,
		event.CreatedAt,
	).Scan(&event.ID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		r.logger.Error("Failed to persist Sumsub webhook event",
			zap.Error(err),
			zap.String("dedupe_key", event.DedupeKey),
			zap.String("applicant_id", event.ApplicantID))
		return false, fmt.Errorf("failed to insert webhook event: %w", err)
	}

	return true, nil
}

// KYCSyncJobRepository stores async jobs for Sumsub provider sync.
type KYCSyncJobRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewKYCSyncJobRepository(db *sql.DB, logger *zap.Logger) *KYCSyncJobRepository {
	return &KYCSyncJobRepository{db: db, logger: logger}
}

// Enqueue inserts a sync job if dedupe key is new.
func (r *KYCSyncJobRepository) Enqueue(ctx context.Context, job *entities.KYCSyncJob) (bool, error) {
	const query = `
		INSERT INTO kyc_sync_jobs (
			id, dedupe_key, applicant_id, correlation_id, event_type, payload,
			status, attempt_count, max_attempts, next_retry_at, last_error, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13
		)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`

	err := r.db.QueryRowContext(ctx, query,
		job.ID,
		job.DedupeKey,
		job.ApplicantID,
		nullIfEmpty(job.CorrelationID),
		job.EventType,
		job.Payload,
		string(job.Status),
		job.AttemptCount,
		job.MaxAttempts,
		job.NextRetryAt,
		job.LastError,
		job.CreatedAt,
		job.UpdatedAt,
	).Scan(&job.ID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		r.logger.Error("Failed to enqueue KYC sync job",
			zap.Error(err),
			zap.String("dedupe_key", job.DedupeKey),
			zap.String("applicant_id", job.ApplicantID))
		return false, fmt.Errorf("failed to enqueue KYC sync job: %w", err)
	}

	return true, nil
}

// EnqueueProviderRetry inserts a per-provider retry job if the dedupe key is new.
func (r *KYCSyncJobRepository) EnqueueProviderRetry(ctx context.Context, userID, provider string, payload []byte) (bool, error) {
	dedupeKey := fmt.Sprintf("provider:%s:user:%s", provider, userID)
	now := time.Now()
	job := &entities.KYCSyncJob{
		ID:           uuid.New(),
		DedupeKey:    dedupeKey,
		EventType:    "provider_retry",
		Payload:      payload,
		Provider:     &provider,
		Status:       entities.KYCSyncJobStatusPending,
		AttemptCount: 0,
		MaxAttempts:  defaultKYCSyncMaxAttempts,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	const query = `
		INSERT INTO kyc_sync_jobs (
			id, dedupe_key, applicant_id, correlation_id, event_type, payload,
			provider, status, attempt_count, max_attempts, next_retry_at, last_error, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13, $14
		)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`

	err := r.db.QueryRowContext(ctx, query,
		job.ID,
		job.DedupeKey,
		"",
		nil,
		job.EventType,
		job.Payload,
		job.Provider,
		string(job.Status),
		job.AttemptCount,
		job.MaxAttempts,
		nil,
		nil,
		job.CreatedAt,
		job.UpdatedAt,
	).Scan(&job.ID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to enqueue provider retry job: %w", err)
	}
	return true, nil
}

const defaultKYCSyncMaxAttempts = 5

func (r *KYCSyncJobRepository) GetNextPendingJobs(ctx context.Context, limit int) ([]*entities.KYCSyncJob, error) {
	const query = `
		SELECT
			id, dedupe_key, applicant_id, correlation_id, event_type, payload,
			provider, status, attempt_count, max_attempts, next_retry_at, last_error, created_at, updated_at
		FROM kyc_sync_jobs
		WHERE status = 'pending'
		   OR (status = 'retry' AND next_retry_at IS NOT NULL AND next_retry_at <= NOW())
		ORDER BY created_at ASC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		r.logger.Error("Failed to fetch pending KYC sync jobs", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch pending KYC sync jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]*entities.KYCSyncJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanKYCSyncJob(rows)
		if scanErr != nil {
			r.logger.Error("Failed to scan KYC sync job", zap.Error(scanErr))
			continue
		}
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating KYC sync jobs: %w", err)
	}

	return jobs, nil
}

// Update stores current job processing status.
func (r *KYCSyncJobRepository) Update(ctx context.Context, job *entities.KYCSyncJob) error {
	const query = `
		UPDATE kyc_sync_jobs
		SET
			status = $1,
			attempt_count = $2,
			next_retry_at = $3,
			last_error = $4,
			updated_at = $5
		WHERE id = $6`

	res, err := r.db.ExecContext(ctx, query,
		string(job.Status),
		job.AttemptCount,
		job.NextRetryAt,
		job.LastError,
		job.UpdatedAt,
		job.ID,
	)
	if err != nil {
		r.logger.Error("Failed to update KYC sync job",
			zap.Error(err),
			zap.String("job_id", job.ID.String()))
		return fmt.Errorf("failed to update KYC sync job: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("kyc sync job not found: %s", job.ID.String())
	}

	return nil
}

func scanKYCSyncJob(scanner interface {
	Scan(dest ...any) error
}) (*entities.KYCSyncJob, error) {
	job := &entities.KYCSyncJob{}
	var correlationID sql.NullString
	var nextRetryAt sql.NullTime
	var lastError sql.NullString

	err := scanner.Scan(
		&job.ID,
		&job.DedupeKey,
		&job.ApplicantID,
		&correlationID,
		&job.EventType,
		&job.Payload,
		&job.Status,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextRetryAt,
		&lastError,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if correlationID.Valid {
		job.CorrelationID = correlationID.String
	}
	if nextRetryAt.Valid {
		job.NextRetryAt = &nextRetryAt.Time
	}
	if lastError.Valid {
		msg := lastError.String
		job.LastError = &msg
	}

	return job, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *KYCSyncJobRepository) GetDLQJobs(ctx context.Context, limit int) ([]*entities.KYCSyncJob, error) {
	const query = `
		SELECT
			id, dedupe_key, applicant_id, correlation_id, event_type, payload,
			status, attempt_count, max_attempts, next_retry_at, last_error, created_at, updated_at
		FROM kyc_sync_jobs
		WHERE status = 'dlq'
		ORDER BY updated_at ASC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		r.logger.Error("Failed to fetch DLQ KYC sync jobs", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch DLQ jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]*entities.KYCSyncJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanKYCSyncJob(rows)
		if scanErr != nil {
			r.logger.Error("Failed to scan DLQ KYC sync job", zap.Error(scanErr))
			continue
		}
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating DLQ KYC sync jobs: %w", err)
	}

	return jobs, nil
}
