package entities

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ============================================================================
// ENUM TYPES
// ============================================================================

// ConversionDirection represents the direction of currency conversion
type ConversionDirection string

const (
	ConversionDirectionUSDCToUSD ConversionDirection = "usdc_to_usd" // Off-ramp
	ConversionDirectionUSDToUSDC ConversionDirection = "usd_to_usdc" // On-ramp
)

// IsValid checks if the conversion direction is valid
func (d ConversionDirection) IsValid() bool {
	switch d {
	case ConversionDirectionUSDCToUSD, ConversionDirectionUSDToUSDC:
		return true
	}
	return false
}

// ConversionJobStatus represents the status of a conversion job
type ConversionJobStatus string

const (
	ConversionJobStatusPending            ConversionJobStatus = "pending"
	ConversionJobStatusProviderSubmitted  ConversionJobStatus = "provider_submitted"
	ConversionJobStatusProviderProcessing ConversionJobStatus = "provider_processing"
	ConversionJobStatusProviderCompleted  ConversionJobStatus = "provider_completed"
	ConversionJobStatusLedgerUpdating     ConversionJobStatus = "ledger_updating"
	ConversionJobStatusCompleted          ConversionJobStatus = "completed"
	ConversionJobStatusFailed             ConversionJobStatus = "failed"
	ConversionJobStatusCancelled          ConversionJobStatus = "cancelled"
)

// IsValid checks if the conversion job status is valid
func (s ConversionJobStatus) IsValid() bool {
	switch s {
	case ConversionJobStatusPending,
		ConversionJobStatusProviderSubmitted,
		ConversionJobStatusProviderProcessing,
		ConversionJobStatusProviderCompleted,
		ConversionJobStatusLedgerUpdating,
		ConversionJobStatusCompleted,
		ConversionJobStatusFailed,
		ConversionJobStatusCancelled:
		return true
	}
	return false
}

// IsFinal returns true if the status is a terminal state
func (s ConversionJobStatus) IsFinal() bool {
	return s == ConversionJobStatusCompleted ||
		s == ConversionJobStatusFailed ||
		s == ConversionJobStatusCancelled
}

// ConversionTrigger represents the reason for a conversion
type ConversionTrigger string

const (
	ConversionTriggerBufferReplenishment ConversionTrigger = "buffer_replenishment"
	ConversionTriggerScheduledRebalance  ConversionTrigger = "scheduled_rebalance"
	ConversionTriggerManual              ConversionTrigger = "manual"
	ConversionTriggerEmergency           ConversionTrigger = "emergency"
)

// IsValid checks if the conversion trigger is valid
func (t ConversionTrigger) IsValid() bool {
	switch t {
	case ConversionTriggerBufferReplenishment,
		ConversionTriggerScheduledRebalance,
		ConversionTriggerManual,
		ConversionTriggerEmergency:
		return true
	}
	return false
}

// ProviderStatus represents the operational status of a conversion provider
type ProviderStatus string

const (
	ProviderStatusActive   ProviderStatus = "active"
	ProviderStatusInactive ProviderStatus = "inactive"
	ProviderStatusDegraded ProviderStatus = "degraded"
)

// IsValid checks if the provider status is valid
func (s ProviderStatus) IsValid() bool {
	switch s {
	case ProviderStatusActive, ProviderStatusInactive, ProviderStatusDegraded:
		return true
	}
	return false
}

// BufferHealthStatus represents the health status of a buffer account
type BufferHealthStatus string

const (
	BufferHealthCriticalLow    BufferHealthStatus = "CRITICAL_LOW"
	BufferHealthBelowTarget    BufferHealthStatus = "BELOW_TARGET"
	BufferHealthHealthy        BufferHealthStatus = "HEALTHY"
	BufferHealthOverCapitalized BufferHealthStatus = "OVER_CAPITALIZED"
)

// ============================================================================
// DOMAIN ENTITIES
// ============================================================================

// ConversionProvider represents a third-party conversion service provider
type ConversionProvider struct {
	ID           uuid.UUID      `db:"id"`
	Name         string         `db:"name"`
	ProviderType string         `db:"provider_type"`
	Priority     int            `db:"priority"`
	Status       ProviderStatus `db:"status"`

	// Configuration
	SupportsUSDCToUSD    bool             `db:"supports_usdc_to_usd"`
	SupportsUSDToUSDC    bool             `db:"supports_usd_to_usdc"`
	MinConversionAmount  decimal.Decimal  `db:"min_conversion_amount"`
	MaxConversionAmount  *decimal.Decimal `db:"max_conversion_amount"`

	// Rate limits
	DailyVolumeLimit *decimal.Decimal `db:"daily_volume_limit"`
	DailyVolumeUsed  decimal.Decimal  `db:"daily_volume_used"`

	// Health tracking
	SuccessCount    int        `db:"success_count"`
	FailureCount    int        `db:"failure_count"`
	LastSuccessAt   *time.Time `db:"last_success_at"`
	LastFailureAt   *time.Time `db:"last_failure_at"`

	// Metadata
	APICredentialsEncrypted *string `db:"api_credentials_encrypted"`
	WebhookSecret           *string `db:"webhook_secret"`
	Notes                   *string `db:"notes"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Validate checks if the conversion provider is valid
func (p *ConversionProvider) Validate() error {
	if p.Name == "" {
		return errors.New("provider name is required")
	}
	if p.ProviderType == "" {
		return errors.New("provider type is required")
	}
	if !p.Status.IsValid() {
		return errors.New("invalid provider status")
	}
	if p.Priority < 1 {
		return errors.New("priority must be at least 1")
	}
	if p.MinConversionAmount.LessThanOrEqual(decimal.Zero) {
		return errors.New("min conversion amount must be positive")
	}
	if p.MaxConversionAmount != nil && p.MaxConversionAmount.LessThanOrEqual(p.MinConversionAmount) {
		return errors.New("max conversion amount must be greater than min")
	}
	return nil
}

// SuccessRate calculates the provider's success rate percentage
func (p *ConversionProvider) SuccessRate() float64 {
	total := p.SuccessCount + p.FailureCount
	if total == 0 {
		return 0
	}
	return float64(p.SuccessCount) / float64(total) * 100
}

// IsHealthy returns true if the provider is healthy enough to use
func (p *ConversionProvider) IsHealthy() bool {
	if p.Status != ProviderStatusActive {
		return false
	}
	// Consider unhealthy if success rate is below 80% with at least 10 attempts
	total := p.SuccessCount + p.FailureCount
	if total >= 10 && p.SuccessRate() < 80.0 {
		return false
	}
	return true
}

// HasCapacity checks if provider has available capacity for the amount
func (p *ConversionProvider) HasCapacity(amount decimal.Decimal) bool {
	if p.MaxConversionAmount != nil && amount.GreaterThan(*p.MaxConversionAmount) {
		return false
	}
	if amount.LessThan(p.MinConversionAmount) {
		return false
	}
	if p.DailyVolumeLimit != nil {
		remaining := p.DailyVolumeLimit.Sub(p.DailyVolumeUsed)
		if amount.GreaterThan(remaining) {
			return false
		}
	}
	return true
}

// BufferThreshold represents the threshold configuration for a buffer account
type BufferThreshold struct {
	ID          uuid.UUID   `db:"id"`
	AccountType AccountType `db:"account_type"`

	// Threshold configuration (in USD equivalent)
	MinThreshold    decimal.Decimal `db:"min_threshold"`
	TargetThreshold decimal.Decimal `db:"target_threshold"`
	MaxThreshold    decimal.Decimal `db:"max_threshold"`

	// Conversion strategy
	ConversionBatchSize decimal.Decimal `db:"conversion_batch_size"`

	// Metadata
	Notes *string `db:"notes"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Validate checks if the buffer threshold is valid
func (b *BufferThreshold) Validate() error {
	if !b.AccountType.IsValid() {
		return errors.New("invalid account type")
	}
	if !b.AccountType.IsSystemAccount() {
		return errors.New("buffer thresholds only apply to system accounts")
	}
	if b.MinThreshold.LessThanOrEqual(decimal.Zero) {
		return errors.New("min threshold must be positive")
	}
	if b.TargetThreshold.LessThanOrEqual(b.MinThreshold) {
		return errors.New("target threshold must be greater than min threshold")
	}
	if b.MaxThreshold.LessThanOrEqual(b.TargetThreshold) {
		return errors.New("max threshold must be greater than target threshold")
	}
	if b.ConversionBatchSize.LessThanOrEqual(decimal.Zero) {
		return errors.New("conversion batch size must be positive")
	}
	return nil
}

// CalculateReplenishmentAmount calculates how much to add to reach target
func (b *BufferThreshold) CalculateReplenishmentAmount(currentBalance decimal.Decimal) decimal.Decimal {
	if currentBalance.GreaterThanOrEqual(b.TargetThreshold) {
		return decimal.Zero
	}
	needed := b.TargetThreshold.Sub(currentBalance)
	// Round up to nearest batch size for efficiency
	batches := needed.Div(b.ConversionBatchSize).Ceil()
	return batches.Mul(b.ConversionBatchSize)
}

// CheckHealthStatus determines the health status of the buffer
func (b *BufferThreshold) CheckHealthStatus(currentBalance decimal.Decimal) BufferHealthStatus {
	if currentBalance.LessThan(b.MinThreshold) {
		return BufferHealthCriticalLow
	}
	if currentBalance.LessThan(b.TargetThreshold) {
		return BufferHealthBelowTarget
	}
	if currentBalance.GreaterThan(b.MaxThreshold) {
		return BufferHealthOverCapitalized
	}
	return BufferHealthHealthy
}

// ConversionJob represents a currency conversion operation
type ConversionJob struct {
	ID              uuid.UUID           `db:"id"`
	Direction       ConversionDirection `db:"direction"`
	Amount          decimal.Decimal     `db:"amount"`
	Status          ConversionJobStatus `db:"status"`
	TriggerReason   ConversionTrigger   `db:"trigger_reason"`

	// Provider tracking
	ProviderID       *uuid.UUID `db:"provider_id"`
	ProviderName     *string    `db:"provider_name"`
	ProviderTxID     *string    `db:"provider_tx_id"`
	ProviderResponse *string    `db:"provider_response"` // JSON string

	// Ledger integration
	LedgerTransactionID *uuid.UUID `db:"ledger_transaction_id"`

	// Source and destination accounts
	SourceAccountID      *uuid.UUID `db:"source_account_id"`
	DestinationAccountID *uuid.UUID `db:"destination_account_id"`

	// Conversion results
	SourceAmount      *decimal.Decimal `db:"source_amount"`
	DestinationAmount *decimal.Decimal `db:"destination_amount"`
	ExchangeRate      *decimal.Decimal `db:"exchange_rate"`
	FeesPaid          *decimal.Decimal `db:"fees_paid"`

	// Timing
	ScheduledAt         *time.Time `db:"scheduled_at"`
	SubmittedAt         *time.Time `db:"submitted_at"`
	ProviderCompletedAt *time.Time `db:"provider_completed_at"`
	CompletedAt         *time.Time `db:"completed_at"`
	FailedAt            *time.Time `db:"failed_at"`

	// Error tracking
	ErrorMessage *string `db:"error_message"`
	ErrorCode    *string `db:"error_code"`
	RetryCount   int     `db:"retry_count"`
	MaxRetries   int     `db:"max_retries"`

	// Idempotency
	IdempotencyKey *string `db:"idempotency_key"`

	// Metadata
	Notes *string `db:"notes"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Validate checks if the conversion job is valid
func (j *ConversionJob) Validate() error {
	if !j.Direction.IsValid() {
		return errors.New("invalid conversion direction")
	}
	if j.Amount.LessThanOrEqual(decimal.Zero) {
		return errors.New("amount must be positive")
	}
	if !j.Status.IsValid() {
		return errors.New("invalid status")
	}
	if !j.TriggerReason.IsValid() {
		return errors.New("invalid trigger reason")
	}
	if j.RetryCount < 0 {
		return errors.New("retry count cannot be negative")
	}
	if j.MaxRetries < 0 {
		return errors.New("max retries cannot be negative")
	}
	return nil
}

// CanRetry returns true if the job can be retried
func (j *ConversionJob) CanRetry() bool {
	return j.Status == ConversionJobStatusFailed &&
		j.RetryCount < j.MaxRetries
}

// IsComplete returns true if the job is in a final state
func (j *ConversionJob) IsComplete() bool {
	return j.Status.IsFinal()
}

// MarkFailed marks the job as failed with an error message
func (j *ConversionJob) MarkFailed(errorMsg, errorCode string) {
	j.Status = ConversionJobStatusFailed
	j.ErrorMessage = &errorMsg
	if errorCode != "" {
		j.ErrorCode = &errorCode
	}
	now := time.Now()
	j.FailedAt = &now
}

// IncrementRetry increments the retry counter
func (j *ConversionJob) IncrementRetry() {
	j.RetryCount++
}

// ConversionJobHistory represents an audit entry for job status changes
type ConversionJobHistory struct {
	ID              uuid.UUID            `db:"id"`
	ConversionJobID uuid.UUID            `db:"conversion_job_id"`
	PreviousStatus  *ConversionJobStatus `db:"previous_status"`
	NewStatus       ConversionJobStatus  `db:"new_status"`
	Notes           *string              `db:"notes"`
	Metadata        *string              `db:"metadata"` // JSON string
	CreatedAt       time.Time            `db:"created_at"`
}

// ============================================================================
// REQUEST/RESPONSE MODELS
// ============================================================================

// CreateConversionJobRequest represents a request to create a new conversion job
type CreateConversionJobRequest struct {
	Direction             ConversionDirection
	Amount                decimal.Decimal
	TriggerReason         ConversionTrigger
	SourceAccountID       uuid.UUID
	DestinationAccountID  uuid.UUID
	ScheduledAt           *time.Time
	IdempotencyKey        string
	Notes                 *string
}

// Validate checks if the request is valid
func (r *CreateConversionJobRequest) Validate() error {
	if !r.Direction.IsValid() {
		return errors.New("invalid conversion direction")
	}
	if r.Amount.LessThanOrEqual(decimal.Zero) {
		return errors.New("amount must be positive")
	}
	if !r.TriggerReason.IsValid() {
		return errors.New("invalid trigger reason")
	}
	if r.SourceAccountID == uuid.Nil {
		return errors.New("source account ID is required")
	}
	if r.DestinationAccountID == uuid.Nil {
		return errors.New("destination account ID is required")
	}
	if r.IdempotencyKey == "" {
		return errors.New("idempotency key is required")
	}
	return nil
}

// UpdateConversionJobStatusRequest represents a request to update job status
type UpdateConversionJobStatusRequest struct {
	JobID              uuid.UUID
	NewStatus          ConversionJobStatus
	ProviderTxID       *string
	ProviderResponse   *string
	SourceAmount       *decimal.Decimal
	DestinationAmount  *decimal.Decimal
	ExchangeRate       *decimal.Decimal
	FeesPaid           *decimal.Decimal
	ErrorMessage       *string
	ErrorCode          *string
}

// Validate checks if the request is valid
func (r *UpdateConversionJobStatusRequest) Validate() error {
	if r.JobID == uuid.Nil {
		return errors.New("job ID is required")
	}
	if !r.NewStatus.IsValid() {
		return errors.New("invalid status")
	}
	return nil
}

// BufferStatus represents the current status of a buffer account
type BufferStatus struct {
	AccountType     AccountType
	CurrentBalance  decimal.Decimal
	MinThreshold    decimal.Decimal
	TargetThreshold decimal.Decimal
	MaxThreshold    decimal.Decimal
	HealthStatus    BufferHealthStatus
	AmountToTarget  decimal.Decimal
}

// NeedsReplenishment returns true if the buffer needs to be replenished
func (b *BufferStatus) NeedsReplenishment() bool {
	return b.HealthStatus == BufferHealthCriticalLow ||
		b.HealthStatus == BufferHealthBelowTarget
}

// ConversionProviderConfig represents configuration for a conversion provider adapter
type ConversionProviderConfig struct {
	ProviderType string
	APIKey       string
	APISecret    string
	BaseURL      string
	WebhookSecret string
	Timeout      time.Duration
}
