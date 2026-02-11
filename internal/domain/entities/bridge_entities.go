package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BridgeStatus represents the status of a bridge transaction
type BridgeStatus string

const (
	BridgeStatusPending   BridgeStatus = "pending"   // Waiting for burn
	BridgeStatusBurning   BridgeStatus = "burning"   // Burn tx submitted
	BridgeStatusAttesting BridgeStatus = "attesting" // Waiting for attestation
	BridgeStatusMinting   BridgeStatus = "minting"   // Mint tx submitted
	BridgeStatusCompleted BridgeStatus = "completed" // Done
	BridgeStatusFailed    BridgeStatus = "failed"    // Error
)

// BridgeTransaction represents a cross-chain USDC transfer via CCTP
type BridgeTransaction struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	SourceChain  string          `json:"source_chain" db:"source_chain"`
	DestChain    string          `json:"dest_chain" db:"dest_chain"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	SourceTxHash string          `json:"source_tx_hash,omitempty" db:"source_tx_hash"`
	MessageHash  string          `json:"message_hash,omitempty" db:"message_hash"`
	Attestation  string          `json:"attestation,omitempty" db:"attestation"`
	DestTxHash   string          `json:"dest_tx_hash,omitempty" db:"dest_tx_hash"`
	DestAddress  string          `json:"dest_address" db:"dest_address"`
	Status       BridgeStatus    `json:"status" db:"status"`
	ErrorMessage string          `json:"error_message,omitempty" db:"error_message"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// BridgeRequest represents a request to initiate a bridge transfer
type BridgeRequest struct {
	UserID      uuid.UUID
	SourceChain string
	Amount      decimal.Decimal
	DestAddress string // Grid account address
}
