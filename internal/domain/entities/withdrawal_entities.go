package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WithdrawalStatus represents the status of a withdrawal
type WithdrawalStatus string

const (
	WithdrawalStatusPending         WithdrawalStatus = "pending"
	WithdrawalStatusAlpacaDebited   WithdrawalStatus = "alpaca_debited"
	WithdrawalStatusDueProcessing   WithdrawalStatus = "due_processing"
	WithdrawalStatusOnChainTransfer WithdrawalStatus = "onchain_transfer"
	WithdrawalStatusCompleted       WithdrawalStatus = "completed"
	WithdrawalStatusFailed          WithdrawalStatus = "failed"
)

// Withdrawal represents a USD to USDC withdrawal
type Withdrawal struct {
	ID                   uuid.UUID        `json:"id" db:"id"`
	UserID               uuid.UUID        `json:"user_id" db:"user_id"`
	AlpacaAccountID      string           `json:"alpaca_account_id" db:"alpaca_account_id"`
	Amount               decimal.Decimal  `json:"amount" db:"amount"`
	DestinationChain     string           `json:"destination_chain" db:"destination_chain"`
	DestinationAddress   string           `json:"destination_address" db:"destination_address"`
	Status               WithdrawalStatus `json:"status" db:"status"`
	AlpacaJournalID      *string          `json:"alpaca_journal_id,omitempty" db:"alpaca_journal_id"`
	DueTransferID        *string          `json:"due_transfer_id,omitempty" db:"due_transfer_id"`
	DueRecipientID       *string          `json:"due_recipient_id,omitempty" db:"due_recipient_id"`
	TxHash               *string          `json:"tx_hash,omitempty" db:"tx_hash"`
	ErrorMessage         *string          `json:"error_message,omitempty" db:"error_message"`
	CreatedAt            time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at" db:"updated_at"`
	CompletedAt          *time.Time       `json:"completed_at,omitempty" db:"completed_at"`
}

// InitiateWithdrawalRequest represents a withdrawal request
type InitiateWithdrawalRequest struct {
	UserID             uuid.UUID       `json:"user_id"`
	AlpacaAccountID    string          `json:"alpaca_account_id"`
	Amount             decimal.Decimal `json:"amount"`
	DestinationChain   string          `json:"destination_chain"`
	DestinationAddress string          `json:"destination_address"`
}

// InitiateWithdrawalResponse represents the response to a withdrawal request
type InitiateWithdrawalResponse struct {
	WithdrawalID uuid.UUID        `json:"withdrawal_id"`
	Status       WithdrawalStatus `json:"status"`
	Message      string           `json:"message"`
}
