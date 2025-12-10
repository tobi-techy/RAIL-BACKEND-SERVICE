package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingJobRepository handles onboarding job database operations
type OnboardingJobRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewOnboardingJobRepository creates a new onboarding job repository
func NewOnboardingJobRepository(db *sql.DB, logger *zap.Logger) *OnboardingJobRepository {
	return &OnboardingJobRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new onboarding job
func (r *OnboardingJobRepository) Create(ctx context.Context, job *entities.OnboardingJob) error {
	query := `
		INSERT INTO onboarding_jobs (
			id, user_id, status, job_type, payload, attempt_count, 
			max_attempts, next_retry_at, error_message, started_at, 
			completed_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	// Convert payload map to JSON for PostgreSQL JSONB storage
	payloadJSON, err := json.Marshal(job.Payload)
	if err != nil {
		r.logger.Error("Failed to marshal job payload to JSON",
			zap.String("user_id", job.UserID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to marshal job payload: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		job.ID,
		job.UserID,
		string(job.Status),
		string(job.JobType),
		payloadJSON,
		job.AttemptCount,
		job.MaxAttempts,
		job.NextRetryAt,
		job.ErrorMessage,
		job.StartedAt,
		job.CompletedAt,
		job.CreatedAt,
		job.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create onboarding job",
			zap.String("user_id", job.UserID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to create onboarding job: %w", err)
	}

	r.logger.Debug("Created onboarding job",
		zap.String("job_id", job.ID.String()),
		zap.String("user_id", job.UserID.String()))

	return nil
}

// GetByID retrieves an onboarding job by ID
func (r *OnboardingJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.OnboardingJob, error) {
	query := `
		SELECT id, user_id, status, job_type, payload, attempt_count,
		       max_attempts, next_retry_at, error_message, started_at,
		       completed_at, created_at, updated_at
		FROM onboarding_jobs 
		WHERE id = $1`

	job := &entities.OnboardingJob{}
	var statusStr, jobTypeStr string
	var nextRetryAt, startedAt, completedAt sql.NullTime
	var errorMessage sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID,
		&job.UserID,
		&statusStr,
		&jobTypeStr,
		&job.Payload,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextRetryAt,
		&errorMessage,
		&startedAt,
		&completedAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("onboarding job not found")
		}
		r.logger.Error("Failed to get onboarding job by ID",
			zap.String("job_id", id.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get onboarding job: %w", err)
	}

	// Convert string fields to enums
	job.Status = entities.OnboardingJobStatus(statusStr)
	job.JobType = entities.OnboardingJobType(jobTypeStr)

	// Handle nullable fields
	if nextRetryAt.Valid {
		job.NextRetryAt = &nextRetryAt.Time
	}
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		job.ErrorMessage = &errorMessage.String
	}

	return job, nil
}

// GetByUserID retrieves an onboarding job by user ID
func (r *OnboardingJobRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.OnboardingJob, error) {
	query := `
		SELECT id, user_id, status, job_type, payload, attempt_count,
		       max_attempts, next_retry_at, error_message, started_at,
		       completed_at, created_at, updated_at
		FROM onboarding_jobs 
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`

	job := &entities.OnboardingJob{}
	var statusStr, jobTypeStr string
	var nextRetryAt, startedAt, completedAt sql.NullTime
	var errorMessage sql.NullString

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&job.ID,
		&job.UserID,
		&statusStr,
		&jobTypeStr,
		&job.Payload,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextRetryAt,
		&errorMessage,
		&startedAt,
		&completedAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("onboarding job not found for user")
		}
		r.logger.Error("Failed to get onboarding job by user ID",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get onboarding job: %w", err)
	}

	// Convert string fields to enums
	job.Status = entities.OnboardingJobStatus(statusStr)
	job.JobType = entities.OnboardingJobType(jobTypeStr)

	// Handle nullable fields
	if nextRetryAt.Valid {
		job.NextRetryAt = &nextRetryAt.Time
	}
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		job.ErrorMessage = &errorMessage.String
	}

	return job, nil
}

// GetPendingJobs retrieves jobs that are eligible for processing
func (r *OnboardingJobRepository) GetPendingJobs(ctx context.Context, limit int) ([]*entities.OnboardingJob, error) {
	query := `
		SELECT id, user_id, status, job_type, payload, attempt_count,
		       max_attempts, next_retry_at, error_message, started_at,
		       completed_at, created_at, updated_at
		FROM onboarding_jobs 
		WHERE (status = 'queued' OR (status = 'retry' AND next_retry_at <= NOW()))
		ORDER BY created_at ASC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		r.logger.Error("Failed to get pending onboarding jobs", zap.Error(err))
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*entities.OnboardingJob
	for rows.Next() {
		job := &entities.OnboardingJob{}
		var statusStr, jobTypeStr string
		var nextRetryAt, startedAt, completedAt sql.NullTime
		var errorMessage sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.UserID,
			&statusStr,
			&jobTypeStr,
			&job.Payload,
			&job.AttemptCount,
			&job.MaxAttempts,
			&nextRetryAt,
			&errorMessage,
			&startedAt,
			&completedAt,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan onboarding job", zap.Error(err))
			continue
		}

		// Convert string fields to enums
		job.Status = entities.OnboardingJobStatus(statusStr)
		job.JobType = entities.OnboardingJobType(jobTypeStr)

		// Handle nullable fields
		if nextRetryAt.Valid {
			job.NextRetryAt = &nextRetryAt.Time
		}
		if startedAt.Valid {
			job.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			job.CompletedAt = &completedAt.Time
		}
		if errorMessage.Valid {
			job.ErrorMessage = &errorMessage.String
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating over onboarding jobs", zap.Error(err))
		return nil, fmt.Errorf("error iterating over jobs: %w", err)
	}

	r.logger.Debug("Retrieved pending onboarding jobs",
		zap.Int("count", len(jobs)))

	return jobs, nil
}

// Update updates an onboarding job
func (r *OnboardingJobRepository) Update(ctx context.Context, job *entities.OnboardingJob) error {
	query := `
		UPDATE onboarding_jobs SET
			status = $2,
			job_type = $3,
			payload = $4,
			attempt_count = $5,
			max_attempts = $6,
			next_retry_at = $7,
			error_message = $8,
			started_at = $9,
			completed_at = $10,
			updated_at = $11
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query,
		job.ID,
		string(job.Status),
		string(job.JobType),
		job.Payload,
		job.AttemptCount,
		job.MaxAttempts,
		job.NextRetryAt,
		job.ErrorMessage,
		job.StartedAt,
		job.CompletedAt,
		job.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to update onboarding job",
			zap.String("job_id", job.ID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to update onboarding job: %w", err)
	}

	r.logger.Debug("Updated onboarding job",
		zap.String("job_id", job.ID.String()),
		zap.String("status", string(job.Status)))

	return nil
}

// Delete deletes an onboarding job
func (r *OnboardingJobRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM onboarding_jobs WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to delete onboarding job",
			zap.String("job_id", id.String()),
			zap.Error(err))
		return fmt.Errorf("failed to delete onboarding job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("onboarding job not found")
	}

	r.logger.Debug("Deleted onboarding job",
		zap.String("job_id", id.String()))

	return nil
}

// GetJobStats returns statistics about onboarding jobs
func (r *OnboardingJobRepository) GetJobStats(ctx context.Context) (map[string]int, error) {
	query := `
		SELECT status, COUNT(*) as count
		FROM onboarding_jobs
		GROUP BY status`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		r.logger.Error("Failed to get onboarding job stats", zap.Error(err))
		return nil, fmt.Errorf("failed to get job stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var status string
		var count int

		if err := rows.Scan(&status, &count); err != nil {
			r.logger.Error("Failed to scan job stats", zap.Error(err))
			continue
		}

		stats[status] = count
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating over job stats", zap.Error(err))
		return nil, fmt.Errorf("error iterating over stats: %w", err)
	}

	return stats, nil
}
