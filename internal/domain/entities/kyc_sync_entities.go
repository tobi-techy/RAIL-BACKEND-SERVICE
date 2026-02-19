package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// KYCSyncJobStatus tracks async KYC provider sync processing.
type KYCSyncJobStatus string

const (
	KYCSyncJobStatusPending    KYCSyncJobStatus = "pending"
	KYCSyncJobStatusProcessing KYCSyncJobStatus = "processing"
	KYCSyncJobStatusRetry      KYCSyncJobStatus = "retry"
	KYCSyncJobStatusCompleted  KYCSyncJobStatus = "completed"
	KYCSyncJobStatusDLQ        KYCSyncJobStatus = "dlq"
)

// SumsubWebhookEvent stores deduplicated webhook events for auditability.
type SumsubWebhookEvent struct {
	ID            uuid.UUID `json:"id" db:"id"`
	DedupeKey     string    `json:"dedupe_key" db:"dedupe_key"`
	ApplicantID   string    `json:"applicant_id" db:"applicant_id"`
	CorrelationID string    `json:"correlation_id" db:"correlation_id"`
	EventType     string    `json:"event_type" db:"event_type"`
	Payload       []byte    `json:"payload" db:"payload"`
	ReceivedAt    time.Time `json:"received_at" db:"received_at"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// KYCSyncJob represents queued async processing for Sumsub -> provider sync.
type KYCSyncJob struct {
	ID            uuid.UUID        `json:"id" db:"id"`
	DedupeKey     string           `json:"dedupe_key" db:"dedupe_key"`
	ApplicantID   string           `json:"applicant_id" db:"applicant_id"`
	CorrelationID string           `json:"correlation_id" db:"correlation_id"`
	EventType     string           `json:"event_type" db:"event_type"`
	Payload       []byte           `json:"payload" db:"payload"`
	Status        KYCSyncJobStatus `json:"status" db:"status"`
	AttemptCount  int              `json:"attempt_count" db:"attempt_count"`
	MaxAttempts   int              `json:"max_attempts" db:"max_attempts"`
	NextRetryAt   *time.Time       `json:"next_retry_at,omitempty" db:"next_retry_at"`
	LastError     *string          `json:"last_error,omitempty" db:"last_error"`
	CreatedAt     time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at" db:"updated_at"`
}

func (j *KYCSyncJob) MarkProcessing() {
	now := time.Now()
	j.Status = KYCSyncJobStatusProcessing
	j.AttemptCount++
	j.UpdatedAt = now
}

func (j *KYCSyncJob) MarkCompleted() {
	now := time.Now()
	j.Status = KYCSyncJobStatusCompleted
	j.NextRetryAt = nil
	j.LastError = nil
	j.UpdatedAt = now
}

func (j *KYCSyncJob) MarkFailed(err error, retryDelay time.Duration) {
	now := time.Now()
	errMsg := err.Error()
	j.LastError = &errMsg
	j.UpdatedAt = now

	if j.AttemptCount < j.MaxAttempts && retryDelay > 0 {
		next := now.Add(retryDelay)
		j.Status = KYCSyncJobStatusRetry
		j.NextRetryAt = &next
		return
	}

	j.Status = KYCSyncJobStatusDLQ
	j.NextRetryAt = nil
}

func (j *KYCSyncJob) Validate() error {
	if j.DedupeKey == "" {
		return fmt.Errorf("dedupe key is required")
	}
	if j.ApplicantID == "" {
		return fmt.Errorf("applicant ID is required")
	}
	if j.EventType == "" {
		return fmt.Errorf("event type is required")
	}
	if len(j.Payload) == 0 {
		return fmt.Errorf("payload is required")
	}
	if j.MaxAttempts <= 0 {
		return fmt.Errorf("max attempts must be positive")
	}
	return nil
}
