package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingFlowRepository implements the onboarding flow repository interface using PostgreSQL
type OnboardingFlowRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewOnboardingFlowRepository creates a new onboarding flow repository
func NewOnboardingFlowRepository(db *sql.DB, logger *zap.Logger) *OnboardingFlowRepository {
	return &OnboardingFlowRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new onboarding flow entry
func (r *OnboardingFlowRepository) Create(ctx context.Context, flow *entities.OnboardingFlow) error {
	query := `
		INSERT INTO onboarding_flows (
			id, user_id, step, status, data, error_message,
			started_at, completed_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)`

	dataJSON, err := mapToJSON(flow.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal onboarding flow data: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		flow.ID,
		flow.UserID,
		string(flow.Step),
		string(flow.Status),
		dataJSON,
		flow.ErrorMessage,
		flow.StartedAt,
		flow.CompletedAt,
		flow.CreatedAt,
		flow.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create onboarding flow", zap.Error(err), zap.String("user_id", flow.UserID.String()))
		return fmt.Errorf("failed to create onboarding flow: %w", err)
	}

	r.logger.Debug("Onboarding flow created successfully", zap.String("flow_id", flow.ID.String()))
	return nil
}

// GetByUserID retrieves all onboarding flows for a user
func (r *OnboardingFlowRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.OnboardingFlow, error) {
	query := `
		SELECT id, user_id, step, status, data, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM onboarding_flows 
		WHERE user_id = $1
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get onboarding flows by user ID", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get onboarding flows: %w", err)
	}
	defer rows.Close()

	var flows []*entities.OnboardingFlow
	for rows.Next() {
		flow := &entities.OnboardingFlow{}
		var startedAt, completedAt sql.NullTime
		var dataJSON []byte

		err := rows.Scan(
			&flow.ID,
			&flow.UserID,
			&flow.Step,
			&flow.Status,
			&dataJSON,
			&flow.ErrorMessage,
			&startedAt,
			&completedAt,
			&flow.CreatedAt,
			&flow.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan onboarding flow", zap.Error(err))
			return nil, fmt.Errorf("failed to scan onboarding flow: %w", err)
		}

		if startedAt.Valid {
			flow.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			flow.CompletedAt = &completedAt.Time
		}

		flow.Data, err = jsonToMap(dataJSON)
		if err != nil {
			r.logger.Error("Failed to unmarshal onboarding flow data", zap.Error(err), zap.String("flow_id", flow.ID.String()))
			return nil, fmt.Errorf("failed to unmarshal onboarding flow data: %w", err)
		}

		flows = append(flows, flow)
	}

	return flows, nil
}

// GetByUserAndStep retrieves an onboarding flow by user ID and step type
func (r *OnboardingFlowRepository) GetByUserAndStep(ctx context.Context, userID uuid.UUID, step entities.OnboardingStepType) (*entities.OnboardingFlow, error) {
	query := `
		SELECT id, user_id, step, status, data, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM onboarding_flows 
		WHERE user_id = $1 AND step = $2
		ORDER BY created_at DESC
		LIMIT 1`

	flow := &entities.OnboardingFlow{}
	var startedAt, completedAt sql.NullTime
	var dataJSON []byte

	err := r.db.QueryRowContext(ctx, query, userID, string(step)).Scan(
		&flow.ID,
		&flow.UserID,
		&flow.Step,
		&flow.Status,
		&dataJSON,
		&flow.ErrorMessage,
		&startedAt,
		&completedAt,
		&flow.CreatedAt,
		&flow.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("onboarding flow not found")
		}
		r.logger.Error("Failed to get onboarding flow by user and step", zap.Error(err),
			zap.String("user_id", userID.String()), zap.String("step", string(step)))
		return nil, fmt.Errorf("failed to get onboarding flow: %w", err)
	}

	if startedAt.Valid {
		flow.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		flow.CompletedAt = &completedAt.Time
	}

	flow.Data, err = jsonToMap(dataJSON)
	if err != nil {
		r.logger.Error("Failed to unmarshal onboarding flow data", zap.Error(err), zap.String("flow_id", flow.ID.String()))
		return nil, fmt.Errorf("failed to unmarshal onboarding flow data: %w", err)
	}

	return flow, nil
}

// Update updates an onboarding flow
func (r *OnboardingFlowRepository) Update(ctx context.Context, flow *entities.OnboardingFlow) error {
	query := `
		UPDATE onboarding_flows SET 
			status = $2, data = $3, error_message = $4, 
			started_at = $5, completed_at = $6, updated_at = $7
		WHERE id = $1`

	dataJSON, err := mapToJSON(flow.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal onboarding flow data: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		flow.ID,
		string(flow.Status),
		dataJSON,
		flow.ErrorMessage,
		flow.StartedAt,
		flow.CompletedAt,
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update onboarding flow", zap.Error(err), zap.String("flow_id", flow.ID.String()))
		return fmt.Errorf("failed to update onboarding flow: %w", err)
	}

	r.logger.Debug("Onboarding flow updated successfully", zap.String("flow_id", flow.ID.String()))
	return nil
}

// GetCompletedSteps returns the list of completed onboarding steps for a user
func (r *OnboardingFlowRepository) GetCompletedSteps(ctx context.Context, userID uuid.UUID) ([]entities.OnboardingStepType, error) {
	query := `
		SELECT DISTINCT step
		FROM onboarding_flows 
		WHERE user_id = $1 AND status = $2
		ORDER BY step`

	rows, err := r.db.QueryContext(ctx, query, userID, string(entities.StepStatusCompleted))
	if err != nil {
		r.logger.Error("Failed to get completed steps", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get completed steps: %w", err)
	}
	defer rows.Close()

	steps := make([]entities.OnboardingStepType, 0)
	for rows.Next() {
		var stepType entities.OnboardingStepType
		if err := rows.Scan(&stepType); err != nil {
			r.logger.Error("Failed to scan step type", zap.Error(err))
			return nil, fmt.Errorf("failed to scan step type: %w", err)
		}
		steps = append(steps, stepType)
	}

	return steps, nil
}

// KYCSubmissionRepository implements the KYC submission repository interface using PostgreSQL
type KYCSubmissionRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewKYCSubmissionRepository creates a new KYC submission repository
func NewKYCSubmissionRepository(db *sql.DB, logger *zap.Logger) *KYCSubmissionRepository {
	return &KYCSubmissionRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new KYC submission
func (r *KYCSubmissionRepository) Create(ctx context.Context, submission *entities.KYCSubmission) error {
	query := `
		INSERT INTO kyc_submissions (
			id, user_id, provider, provider_ref, submission_type, status, submitted_at, 
			reviewed_at, expires_at, rejection_reasons, verification_data, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	rejectionReasonsArray := pq.StringArray(submission.RejectionReasons)
	if rejectionReasonsArray == nil {
		rejectionReasonsArray = pq.StringArray{}
	}
	verificationDataJSON, err := mapToJSON(submission.VerificationData)
	if err != nil {
		return fmt.Errorf("failed to marshal KYC verification data: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		submission.ID,
		submission.UserID,
		submission.Provider,
		submission.ProviderRef,
		submission.SubmissionType,
		string(submission.Status),
		submission.SubmittedAt,
		submission.ReviewedAt,
		submission.ExpiresAt,
		rejectionReasonsArray,
		verificationDataJSON,
		submission.CreatedAt,
		submission.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create KYC submission", zap.Error(err), zap.String("user_id", submission.UserID.String()))
		return fmt.Errorf("failed to create KYC submission: %w", err)
	}

	r.logger.Debug("KYC submission created successfully", zap.String("submission_id", submission.ID.String()))
	return nil
}

// GetByUserID retrieves all KYC submissions for a user
func (r *KYCSubmissionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.KYCSubmission, error) {
	query := `
		SELECT id, user_id, provider, provider_ref, submission_type, status, submitted_at, 
		       reviewed_at, expires_at, rejection_reasons, verification_data, created_at, updated_at
		FROM kyc_submissions 
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get KYC submissions by user ID", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get KYC submissions: %w", err)
	}
	defer rows.Close()

	var submissions []*entities.KYCSubmission
	for rows.Next() {
		submission := &entities.KYCSubmission{}
		var reviewedAt, expiresAt sql.NullTime
		var rejectionReasons pq.StringArray
		var verificationDataJSON []byte

		err := rows.Scan(
			&submission.ID,
			&submission.UserID,
			&submission.Provider,
			&submission.ProviderRef,
			&submission.SubmissionType,
			&submission.Status,
			&submission.SubmittedAt,
			&reviewedAt,
			&expiresAt,
			&rejectionReasons,
			&verificationDataJSON,
			&submission.CreatedAt,
			&submission.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan KYC submission", zap.Error(err))
			return nil, fmt.Errorf("failed to scan KYC submission: %w", err)
		}

		if reviewedAt.Valid {
			submission.ReviewedAt = &reviewedAt.Time
		}
		if expiresAt.Valid {
			submission.ExpiresAt = &expiresAt.Time
		}

		submission.RejectionReasons = append([]string(nil), rejectionReasons...)

		submission.VerificationData, err = jsonToMap(verificationDataJSON)
		if err != nil {
			r.logger.Error("Failed to unmarshal KYC verification data", zap.Error(err), zap.String("submission_id", submission.ID.String()))
			return nil, fmt.Errorf("failed to unmarshal KYC verification data: %w", err)
		}

		submissions = append(submissions, submission)
	}

	return submissions, nil
}

// GetByProviderRef retrieves a KYC submission by provider reference
func (r *KYCSubmissionRepository) GetByProviderRef(ctx context.Context, providerRef string) (*entities.KYCSubmission, error) {
	query := `
		SELECT id, user_id, provider, provider_ref, submission_type, status, submitted_at, 
		       reviewed_at, expires_at, rejection_reasons, verification_data, created_at, updated_at
		FROM kyc_submissions 
		WHERE provider_ref = $1`

	submission := &entities.KYCSubmission{}
	var reviewedAt, expiresAt sql.NullTime
	var rejectionReasons pq.StringArray
	var verificationDataJSON []byte

	err := r.db.QueryRowContext(ctx, query, providerRef).Scan(
		&submission.ID,
		&submission.UserID,
		&submission.Provider,
		&submission.ProviderRef,
		&submission.SubmissionType,
		&submission.Status,
		&submission.SubmittedAt,
		&reviewedAt,
		&expiresAt,
		&rejectionReasons,
		&verificationDataJSON,
		&submission.CreatedAt,
		&submission.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("KYC submission not found")
		}
		r.logger.Error("Failed to get KYC submission by provider ref", zap.Error(err), zap.String("provider_ref", providerRef))
		return nil, fmt.Errorf("failed to get KYC submission: %w", err)
	}

	if reviewedAt.Valid {
		submission.ReviewedAt = &reviewedAt.Time
	}
	if expiresAt.Valid {
		submission.ExpiresAt = &expiresAt.Time
	}

	submission.RejectionReasons = append([]string(nil), rejectionReasons...)

	submission.VerificationData, err = jsonToMap(verificationDataJSON)
	if err != nil {
		r.logger.Error("Failed to unmarshal KYC verification data", zap.Error(err), zap.String("submission_id", submission.ID.String()))
		return nil, fmt.Errorf("failed to unmarshal KYC verification data: %w", err)
	}

	return submission, nil
}

// Update updates a KYC submission
func (r *KYCSubmissionRepository) Update(ctx context.Context, submission *entities.KYCSubmission) error {
	rejectionReasonsArray := pq.StringArray(submission.RejectionReasons)
	if rejectionReasonsArray == nil {
		rejectionReasonsArray = pq.StringArray{}
	}
	verificationDataJSON, err := mapToJSON(submission.VerificationData)
	if err != nil {
		return fmt.Errorf("failed to marshal KYC verification data: %w", err)
	}

	query := `
		UPDATE kyc_submissions SET 
			status = $2, reviewed_at = $3, rejection_reasons = $4, 
			verification_data = $5, updated_at = $6
		WHERE id = $1`

	_, err = r.db.ExecContext(ctx, query,
		submission.ID,
		string(submission.Status),
		submission.ReviewedAt,
		rejectionReasonsArray,
		verificationDataJSON,
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update KYC submission", zap.Error(err), zap.String("submission_id", submission.ID.String()))
		return fmt.Errorf("failed to update KYC submission: %w", err)
	}

	r.logger.Debug("KYC submission updated successfully", zap.String("submission_id", submission.ID.String()))
	return nil
}

// GetLatestByUserID retrieves the most recent KYC submission for a user
func (r *KYCSubmissionRepository) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.KYCSubmission, error) {
	query := `
		SELECT id, user_id, provider, provider_ref, submission_type, status, submitted_at, 
		       reviewed_at, expires_at, rejection_reasons, verification_data, created_at, updated_at
		FROM kyc_submissions 
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`

	submission := &entities.KYCSubmission{}
	var reviewedAt, expiresAt sql.NullTime
	var rejectionReasons pq.StringArray
	var verificationDataJSON []byte

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&submission.ID,
		&submission.UserID,
		&submission.Provider,
		&submission.ProviderRef,
		&submission.SubmissionType,
		&submission.Status,
		&submission.SubmittedAt,
		&reviewedAt,
		&expiresAt,
		&rejectionReasons,
		&verificationDataJSON,
		&submission.CreatedAt,
		&submission.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("KYC submission not found")
		}
		r.logger.Error("Failed to get latest KYC submission by user ID", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get KYC submission: %w", err)
	}

	if reviewedAt.Valid {
		submission.ReviewedAt = &reviewedAt.Time
	}
	if expiresAt.Valid {
		submission.ExpiresAt = &expiresAt.Time
	}

	submission.RejectionReasons = append([]string(nil), rejectionReasons...)

	submission.VerificationData, err = jsonToMap(verificationDataJSON)
	if err != nil {
		r.logger.Error("Failed to unmarshal KYC verification data", zap.Error(err), zap.String("submission_id", submission.ID.String()))
		return nil, fmt.Errorf("failed to unmarshal KYC verification data: %w", err)
	}

	return submission, nil
}

// WalletProvisioningJobRepository implements the wallet provisioning job repository interface using PostgreSQL
type WalletProvisioningJobRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewWalletProvisioningJobRepository creates a new wallet provisioning job repository
func NewWalletProvisioningJobRepository(db *sql.DB, logger *zap.Logger) *WalletProvisioningJobRepository {
	return &WalletProvisioningJobRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new wallet provisioning job
func (r *WalletProvisioningJobRepository) Create(ctx context.Context, job *entities.WalletProvisioningJob) error {
	chainsArray := pq.StringArray(job.Chains)
	if chainsArray == nil {
		chainsArray = pq.StringArray{}
	}

	query := `
		INSERT INTO wallet_provisioning_jobs (
			id, user_id, chains, status, attempt_count, max_attempts,
			error_message, next_retry_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)`

	_, err := r.db.ExecContext(ctx, query,
		job.ID,
		job.UserID,
		chainsArray,
		string(job.Status),
		job.AttemptCount,
		job.MaxAttempts,
		job.ErrorMessage,
		job.NextRetryAt,
		job.CreatedAt,
		job.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create wallet provisioning job", zap.Error(err), zap.String("user_id", job.UserID.String()))
		return fmt.Errorf("failed to create wallet provisioning job: %w", err)
	}

	r.logger.Debug("Wallet provisioning job created successfully", zap.String("job_id", job.ID.String()))
	return nil
}

// GetByID retrieves a wallet provisioning job by ID
func (r *WalletProvisioningJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error) {
	query := `
		SELECT id, user_id, chains, status, attempt_count, max_attempts,
		       error_message, next_retry_at, created_at, updated_at
		FROM wallet_provisioning_jobs 
		WHERE id = $1`

	job := &entities.WalletProvisioningJob{}
	var chains pq.StringArray
	var nextRetryAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID,
		&job.UserID,
		&chains,
		&job.Status,
		&job.AttemptCount,
		&job.MaxAttempts,
		&job.ErrorMessage,
		&nextRetryAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet provisioning job not found")
		}
		r.logger.Error("Failed to get wallet provisioning job by ID", zap.Error(err), zap.String("job_id", id.String()))
		return nil, fmt.Errorf("failed to get wallet provisioning job: %w", err)
	}

	job.Chains = append([]string(nil), chains...)

	if nextRetryAt.Valid {
		job.NextRetryAt = &nextRetryAt.Time
	}

	return job, nil
}

// GetByUserID retrieves a wallet provisioning job by user ID
func (r *WalletProvisioningJobRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error) {
	query := `
		SELECT id, user_id, chains, status, attempt_count, max_attempts,
		       error_message, next_retry_at, created_at, updated_at
		FROM wallet_provisioning_jobs 
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`

	job := &entities.WalletProvisioningJob{}
	var chains pq.StringArray
	var nextRetryAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&job.ID,
		&job.UserID,
		&chains,
		&job.Status,
		&job.AttemptCount,
		&job.MaxAttempts,
		&job.ErrorMessage,
		&nextRetryAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet provisioning job not found")
		}
		r.logger.Error("Failed to get wallet provisioning job by user ID", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get wallet provisioning job: %w", err)
	}

	job.Chains = append([]string(nil), chains...)

	if nextRetryAt.Valid {
		job.NextRetryAt = &nextRetryAt.Time
	}

	return job, nil
}

// GetRetryableJobs retrieves wallet provisioning jobs that can be retried
func (r *WalletProvisioningJobRepository) GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error) {
	query := `
		SELECT id, user_id, chains, status, attempt_count, max_attempts,
		       error_message, next_retry_at, created_at, updated_at
		FROM wallet_provisioning_jobs 
		WHERE status IN ($1, $2)
		   AND attempt_count < max_attempts 
		   AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY created_at ASC
		LIMIT $3`

	rows, err := r.db.QueryContext(ctx, query,
		string(entities.ProvisioningStatusFailed),
		string(entities.ProvisioningStatusRetry),
		limit)
	if err != nil {
		r.logger.Error("Failed to get retryable jobs", zap.Error(err))
		return nil, fmt.Errorf("failed to get retryable jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*entities.WalletProvisioningJob
	for rows.Next() {
		job := &entities.WalletProvisioningJob{}
		var chains pq.StringArray
		var nextRetryAt sql.NullTime

		err := rows.Scan(
			&job.ID,
			&job.UserID,
			&chains,
			&job.Status,
			&job.AttemptCount,
			&job.MaxAttempts,
			&job.ErrorMessage,
			&nextRetryAt,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet provisioning job", zap.Error(err))
			return nil, fmt.Errorf("failed to scan wallet provisioning job: %w", err)
		}

		job.Chains = append([]string(nil), chains...)

		if nextRetryAt.Valid {
			job.NextRetryAt = &nextRetryAt.Time
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// Update updates a wallet provisioning job
func (r *WalletProvisioningJobRepository) Update(ctx context.Context, job *entities.WalletProvisioningJob) error {
	chainsArray := pq.StringArray(job.Chains)
	if chainsArray == nil {
		chainsArray = pq.StringArray{}
	}

	query := `
		UPDATE wallet_provisioning_jobs SET 
			chains = $2, status = $3, attempt_count = $4, max_attempts = $5,
			error_message = $6, next_retry_at = $7, updated_at = $8
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query,
		job.ID,
		chainsArray,
		string(job.Status),
		job.AttemptCount,
		job.MaxAttempts,
		job.ErrorMessage,
		job.NextRetryAt,
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update wallet provisioning job", zap.Error(err), zap.String("job_id", job.ID.String()))
		return fmt.Errorf("failed to update wallet provisioning job: %w", err)
	}

	r.logger.Debug("Wallet provisioning job updated successfully", zap.String("job_id", job.ID.String()))
	return nil
}

// JSON utility functions
func mapToJSON(data map[string]any) ([]byte, error) {
	if data == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(data)
}

func jsonToMap(data []byte) (map[string]any, error) {
	if len(data) == 0 || string(data) == "null" {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	if result == nil {
		result = map[string]any{}
	}

	return result, nil
}
