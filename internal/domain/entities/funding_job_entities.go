package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FundingEventJobStatus represents the status of a funding event processing job
type FundingEventJobStatus string

const (
	JobStatusPending    FundingEventJobStatus = "pending"
	JobStatusProcessing FundingEventJobStatus = "processing"
	JobStatusCompleted  FundingEventJobStatus = "completed"
	JobStatusFailed     FundingEventJobStatus = "failed"
	JobStatusDLQ        FundingEventJobStatus = "dlq" // Dead Letter Queue
)

// FundingEventErrorType categorizes errors for retry decisions
type FundingEventErrorType string

const (
	ErrorTypeTransient  FundingEventErrorType = "transient"   // Retry-able (network, timeout, 5xx)
	ErrorTypePermanent  FundingEventErrorType = "permanent"   // Non-retry-able (validation, 4xx)
	ErrorTypeUnknown    FundingEventErrorType = "unknown"     // Default, treat as transient
	ErrorTypeRPCFailure FundingEventErrorType = "rpc_failure" // RPC/chain query failed
)

// FundingEventJob represents a queued webhook event for processing
type FundingEventJob struct {
	ID           uuid.UUID             `json:"id" db:"id"`
	TxHash       string                `json:"tx_hash" db:"tx_hash"`
	Chain        Chain                 `json:"chain" db:"chain"`
	Token        Stablecoin            `json:"token" db:"token"`
	Amount       decimal.Decimal       `json:"amount" db:"amount"`
	ToAddress    string                `json:"to_address" db:"to_address"`
	Status       FundingEventJobStatus `json:"status" db:"status"`
	AttemptCount int                   `json:"attempt_count" db:"attempt_count"`
	MaxAttempts  int                   `json:"max_attempts" db:"max_attempts"`

	// Error tracking
	LastError     *string                `json:"last_error,omitempty" db:"last_error"`
	ErrorType     *FundingEventErrorType `json:"error_type,omitempty" db:"error_type"`
	FailureReason *string                `json:"failure_reason,omitempty" db:"failure_reason"`

	// Timing
	FirstSeenAt   time.Time  `json:"first_seen_at" db:"first_seen_at"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty" db:"last_attempt_at"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty" db:"next_retry_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	MovedToDLQAt  *time.Time `json:"moved_to_dlq_at,omitempty" db:"moved_to_dlq_at"`

	// Metadata
	WebhookPayload map[string]interface{} `json:"webhook_payload,omitempty" db:"webhook_payload"`
	ProcessingLogs []ProcessingLogEntry   `json:"processing_logs,omitempty" db:"processing_logs"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at" db:"updated_at"`
}

// ProcessingLogEntry tracks individual processing attempts
type ProcessingLogEntry struct {
	Timestamp  time.Time              `json:"timestamp"`
	Attempt    int                    `json:"attempt"`
	Status     string                 `json:"status"`
	Error      *string                `json:"error,omitempty"`
	ErrorType  *FundingEventErrorType `json:"error_type,omitempty"`
	DurationMs int64                  `json:"duration_ms"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// CanRetry checks if the job is eligible for retry
func (j *FundingEventJob) CanRetry() bool {
	return j.Status == JobStatusFailed &&
		j.AttemptCount < j.MaxAttempts &&
		j.ErrorType != nil &&
		*j.ErrorType != ErrorTypePermanent
}

// ShouldMoveToDLQ checks if job should be moved to DLQ
func (j *FundingEventJob) ShouldMoveToDLQ() bool {
	return (j.Status == JobStatusFailed && j.AttemptCount >= j.MaxAttempts) ||
		(j.ErrorType != nil && *j.ErrorType == ErrorTypePermanent)
}

// MarkProcessing marks the job as currently being processed
func (j *FundingEventJob) MarkProcessing() {
	now := time.Now()
	j.Status = JobStatusProcessing
	j.AttemptCount++
	j.LastAttemptAt = &now
	j.UpdatedAt = now
}

// MarkCompleted marks the job as successfully completed
func (j *FundingEventJob) MarkCompleted() {
	now := time.Now()
	j.Status = JobStatusCompleted
	j.CompletedAt = &now
	j.NextRetryAt = nil
	j.UpdatedAt = now
}

// MarkFailed marks the job as failed and calculates next retry time
func (j *FundingEventJob) MarkFailed(err error, errorType FundingEventErrorType, nextRetryDelay time.Duration) {
	now := time.Now()
	errMsg := err.Error()

	j.Status = JobStatusFailed
	j.LastError = &errMsg
	j.ErrorType = &errorType
	j.UpdatedAt = now

	// Calculate next retry time if eligible
	if j.CanRetry() && errorType != ErrorTypePermanent {
		nextRetry := now.Add(nextRetryDelay)
		j.NextRetryAt = &nextRetry
	} else if j.ShouldMoveToDLQ() {
		// Mark for DLQ
		j.Status = JobStatusDLQ
		j.MovedToDLQAt = &now
		j.NextRetryAt = nil

		// Generate failure reason
		reason := fmt.Sprintf("Exhausted %d attempts or permanent error: %s", j.AttemptCount, errMsg)
		j.FailureReason = &reason
	}
}

// AddProcessingLog adds a log entry for the current attempt
func (j *FundingEventJob) AddProcessingLog(entry ProcessingLogEntry) {
	if j.ProcessingLogs == nil {
		j.ProcessingLogs = make([]ProcessingLogEntry, 0)
	}
	j.ProcessingLogs = append(j.ProcessingLogs, entry)
}

// GetRetryDelay calculates exponential backoff with jitter
func (j *FundingEventJob) GetRetryDelay() time.Duration {
	// Exponential backoff: base * 2^attempt
	baseDelay := 2 * time.Second
	maxDelay := 30 * time.Minute

	// Calculate exponential delay
	attempt := j.AttemptCount
	if attempt > 10 {
		attempt = 10 // Cap to prevent overflow
	}

	delay := baseDelay * (1 << uint(attempt)) // 2^attempt

	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter (±20%)
	jitter := time.Duration(float64(delay) * 0.2 * (2.0*randomFloat() - 1.0))
	delay += jitter

	if delay < 0 {
		delay = baseDelay
	}

	return delay
}

// Simple random float for jitter (0.0 to 1.0)
func randomFloat() float64 {
	// Simple pseudo-random based on time
	return float64(time.Now().UnixNano()%1000) / 1000.0
}

// Validate validates the funding event job
func (j *FundingEventJob) Validate() error {
	if j.TxHash == "" {
		return fmt.Errorf("tx_hash is required")
	}

	if j.Chain == "" {
		return fmt.Errorf("chain is required")
	}

	if j.Token == "" {
		return fmt.Errorf("token is required")
	}

	if j.Amount.IsZero() || j.Amount.IsNegative() {
		return fmt.Errorf("amount must be positive")
	}

	if j.ToAddress == "" {
		return fmt.Errorf("to_address is required")
	}

	if j.MaxAttempts <= 0 {
		return fmt.Errorf("max_attempts must be positive")
	}

	return nil
}

// ReconciliationCandidate represents a deposit pending reconciliation
type ReconciliationCandidate struct {
	DepositID       uuid.UUID       `json:"deposit_id" db:"deposit_id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	TxHash          string          `json:"tx_hash" db:"tx_hash"`
	Chain           Chain           `json:"chain" db:"chain"`
	Token           Stablecoin      `json:"token" db:"token"`
	Amount          decimal.Decimal `json:"amount" db:"amount"`
	ToAddress       string          `json:"to_address" db:"to_address"`
	Status          string          `json:"status" db:"status"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	PendingDuration time.Duration   `json:"pending_duration"`
}

// ShouldReconcile determines if a deposit should be reconciled
func (c *ReconciliationCandidate) ShouldReconcile(threshold time.Duration) bool {
	return c.Status == "pending" && c.PendingDuration > threshold
}

// WebhookMetrics tracks webhook processing metrics
type WebhookMetrics struct {
	TotalReceived      int64         `json:"total_received"`
	TotalProcessed     int64         `json:"total_processed"`
	TotalFailed        int64         `json:"total_failed"`
	TotalDLQ           int64         `json:"total_dlq"`
	SuccessRate        float64       `json:"success_rate"`
	AverageLatency     time.Duration `json:"average_latency"`
	AverageRetryCount  float64       `json:"average_retry_count"`
	DLQDepth           int64         `json:"dlq_depth"`
	PendingCount       int64         `json:"pending_count"`
	ReconciliationRuns int64         `json:"reconciliation_runs"`
	RecoveredDeposits  int64         `json:"recovered_deposits"`
}

// CalculateSuccessRate calculates the success rate
func (m *WebhookMetrics) CalculateSuccessRate() {
	if m.TotalReceived > 0 {
		m.SuccessRate = float64(m.TotalProcessed) / float64(m.TotalReceived) * 100.0
	}
}
