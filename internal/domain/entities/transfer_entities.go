package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TransferStatus represents the status of a transfer operation
type TransferStatus string

const (
	TransferStatusInitiated    TransferStatus = "initiated"
	TransferStatusDepositing   TransferStatus = "depositing"   // USDC being sent to Circle wallet
	TransferStatusDeposited    TransferStatus = "deposited"    // USDC confirmed in Circle wallet
	TransferStatusOffRamping   TransferStatus = "off_ramping"  // Converting USDC to fiat
	TransferStatusOffRamped    TransferStatus = "off_ramped"   // Fiat received from Circle
	TransferStatusTransferring TransferStatus = "transferring" // Moving fiat to Alpaca
	TransferStatusCompleted    TransferStatus = "completed"    // Funds available in Alpaca
	TransferStatusFailed       TransferStatus = "failed"
	TransferStatusCancelled    TransferStatus = "cancelled"
)

// TransferType represents the type of transfer
type TransferType string

const (
	TransferTypeUSDCToFiat TransferType = "usdc_to_fiat" // USDC deposit to fiat
	TransferTypeFiatToUSD  TransferType = "fiat_to_usd"  // Fiat to Alpaca USD
)

// Transfer represents a complete transfer operation from USDC deposit to Alpaca funding
type Transfer struct {
	ID     uuid.UUID      `json:"id" db:"id"`
	UserID uuid.UUID      `json:"user_id" db:"user_id"`
	Type   TransferType   `json:"type" db:"type"`
	Status TransferStatus `json:"status" db:"status"`

	// Source details (USDC deposit)
	SourceChain    Chain           `json:"source_chain" db:"source_chain"`
	SourceTxHash   *string         `json:"source_tx_hash" db:"source_tx_hash"`
	SourceWalletID *string         `json:"source_wallet_id" db:"source_wallet_id"` // Circle wallet ID
	SourceAmount   decimal.Decimal `json:"source_amount" db:"source_amount"`
	SourceToken    Stablecoin      `json:"source_token" db:"source_token"`

	// Destination details (Alpaca funding)
	DestAccountID *string          `json:"dest_account_id" db:"dest_account_id"` // Alpaca account ID
	DestAmount    *decimal.Decimal `json:"dest_amount" db:"dest_amount"`
	DestCurrency  string           `json:"dest_currency" db:"dest_currency"` // USD

	// Processing details
	IdempotencyKey string           `json:"idempotency_key" db:"idempotency_key"`
	ExchangeRate   *decimal.Decimal `json:"exchange_rate" db:"exchange_rate"` // USDC to USD rate
	Fees           *decimal.Decimal `json:"fees" db:"fees"`                   // Total fees incurred

	// Circle-specific fields
	CircleTransferID *string `json:"circle_transfer_id" db:"circle_transfer_id"`
	CirclePayoutID   *string `json:"circle_payout_id" db:"circle_payout_id"`

	// Alpaca-specific fields
	AlpacaTransferID *string `json:"alpaca_transfer_id" db:"alpaca_transfer_id"`

	// Error handling
	ErrorMessage *string `json:"error_message" db:"error_message"`
	ErrorType    *string `json:"error_type" db:"error_type"`

	// Timing
	InitiatedAt time.Time  `json:"initiated_at" db:"initiated_at"`
	DepositedAt *time.Time `json:"deposited_at" db:"deposited_at"`
	OffRampedAt *time.Time `json:"off_ramped_at" db:"off_ramped_at"`
	CompletedAt *time.Time `json:"completed_at" db:"completed_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`

	// Metadata
	WebhookPayload map[string]interface{}       `json:"webhook_payload,omitempty" db:"webhook_payload"`
	ProcessingLogs []TransferProcessingLogEntry `json:"processing_logs,omitempty" db:"processing_logs"`
}

// ProcessingLogEntry tracks individual processing steps
type TransferProcessingLogEntry struct {
	Timestamp  time.Time              `json:"timestamp"`
	Step       string                 `json:"step"`
	Status     string                 `json:"status"`
	Error      *string                `json:"error,omitempty"`
	ErrorType  *string                `json:"error_type,omitempty"`
	DurationMs int64                  `json:"duration_ms"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Validate validates the transfer entity
func (t *Transfer) Validate() error {
	if t.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if t.SourceAmount.IsZero() || t.SourceAmount.IsNegative() {
		return fmt.Errorf("source_amount must be positive")
	}

	if t.SourceToken != StablecoinUSDC {
		return fmt.Errorf("only USDC transfers are currently supported")
	}

	if t.IdempotencyKey == "" {
		return fmt.Errorf("idempotency_key is required")
	}

	return nil
}

// IsComplete checks if the transfer is in a terminal state
func (t *Transfer) IsComplete() bool {
	return t.Status == TransferStatusCompleted ||
		t.Status == TransferStatusFailed ||
		t.Status == TransferStatusCancelled
}

// CanRetry checks if the transfer can be retried
func (t *Transfer) CanRetry() bool {
	return t.Status == TransferStatusFailed &&
		t.ErrorType != nil &&
		*t.ErrorType != "permanent_failure"
}

// AddProcessingLog adds a log entry for tracking
func (t *Transfer) AddProcessingLog(entry TransferProcessingLogEntry) {
	if t.ProcessingLogs == nil {
		t.ProcessingLogs = make([]TransferProcessingLogEntry, 0)
	}
	t.ProcessingLogs = append(t.ProcessingLogs, entry)
}

// UpdateStatus updates the transfer status with timestamps
func (t *Transfer) UpdateStatus(newStatus TransferStatus) {
	t.Status = newStatus
	t.UpdatedAt = time.Now()

	switch newStatus {
	case TransferStatusDeposited:
		now := time.Now()
		t.DepositedAt = &now
	case TransferStatusOffRamped:
		now := time.Now()
		t.OffRampedAt = &now
	case TransferStatusCompleted:
		now := time.Now()
		t.CompletedAt = &now
	}
}
