package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OnboardingJobStatus represents the status of an onboarding job
type OnboardingJobStatus string

const (
	OnboardingJobStatusQueued     OnboardingJobStatus = "queued"
	OnboardingJobStatusInProgress OnboardingJobStatus = "in_progress"
	OnboardingJobStatusCompleted  OnboardingJobStatus = "completed"
	OnboardingJobStatusFailed     OnboardingJobStatus = "failed"
	OnboardingJobStatusRetry      OnboardingJobStatus = "retry"
)

// OnboardingJobType represents the type of onboarding job
type OnboardingJobType string

const (
	OnboardingJobTypeFullOnboarding OnboardingJobType = "full_onboarding"
	OnboardingJobTypeKYCOnly        OnboardingJobType = "kyc_only"
	OnboardingJobTypeWalletOnly     OnboardingJobType = "wallet_only"
)

// OnboardingJob represents an async onboarding job
type OnboardingJob struct {
	ID           uuid.UUID              `json:"id" db:"id"`
	UserID       uuid.UUID              `json:"userId" db:"user_id"`
	Status       OnboardingJobStatus    `json:"status" db:"status"`
	JobType      OnboardingJobType      `json:"jobType" db:"job_type"`
	Payload      map[string]interface{} `json:"payload" db:"payload"`
	AttemptCount int                    `json:"attemptCount" db:"attempt_count"`
	MaxAttempts  int                    `json:"maxAttempts" db:"max_attempts"`
	NextRetryAt  *time.Time             `json:"nextRetryAt" db:"next_retry_at"`
	ErrorMessage *string                `json:"errorMessage" db:"error_message"`
	StartedAt    *time.Time             `json:"startedAt" db:"started_at"`
	CompletedAt  *time.Time             `json:"completedAt" db:"completed_at"`
	CreatedAt    time.Time              `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at" db:"updated_at"`
}

// MarkStarted marks the job as started
func (j *OnboardingJob) MarkStarted() {
	now := time.Now()
	j.Status = OnboardingJobStatusInProgress
	j.StartedAt = &now
	j.AttemptCount++
	j.UpdatedAt = now
}

// MarkCompleted marks the job as completed
func (j *OnboardingJob) MarkCompleted() {
	now := time.Now()
	j.Status = OnboardingJobStatusCompleted
	j.CompletedAt = &now
	j.NextRetryAt = nil
	j.ErrorMessage = nil
	j.UpdatedAt = now
}

// MarkFailed marks the job as failed with retry logic
func (j *OnboardingJob) MarkFailed(errorMsg string, retryDelay time.Duration) {
	now := time.Now()
	j.ErrorMessage = &errorMsg
	j.UpdatedAt = now

	if j.AttemptCount < j.MaxAttempts && retryDelay > 0 {
		j.Status = OnboardingJobStatusRetry
		retryTime := now.Add(retryDelay)
		j.NextRetryAt = &retryTime
	} else {
		j.Status = OnboardingJobStatusFailed
		j.NextRetryAt = nil
	}
}

// IsRetryable checks if the job can be retried
func (j *OnboardingJob) IsRetryable() bool {
	return j.Status == OnboardingJobStatusRetry &&
		j.NextRetryAt != nil &&
		time.Now().After(*j.NextRetryAt) &&
		j.AttemptCount < j.MaxAttempts
}

// IsEligibleForProcessing checks if the job is eligible for processing
func (j *OnboardingJob) IsEligibleForProcessing() bool {
	return j.Status == OnboardingJobStatusQueued || j.IsRetryable()
}

// Validate validates the onboarding job
func (j *OnboardingJob) Validate() error {
	if j.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if j.JobType == "" {
		return fmt.Errorf("job type is required")
	}

	if j.MaxAttempts <= 0 {
		return fmt.Errorf("max attempts must be greater than 0")
	}

	return nil
}

// OnboardingJobPayload represents the payload for different job types
type OnboardingJobPayload struct {
	UserEmail    string                 `json:"user_email,omitempty"`
	UserPhone    string                 `json:"user_phone,omitempty"`
	KYCData      map[string]interface{} `json:"kyc_data,omitempty"`
	WalletChains []string               `json:"wallet_chains,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
