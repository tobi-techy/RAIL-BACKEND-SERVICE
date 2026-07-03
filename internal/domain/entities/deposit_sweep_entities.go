package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	DepositSweepStatusPending        = "pending"
	DepositSweepStatusInProgress     = "in_progress"
	DepositSweepStatusCompleted      = "completed"
	DepositSweepStatusFailed         = "failed"
	DepositSweepStatusFailedTerminal = "failed_terminal"
)

// DepositSweep represents an auto-sweep from a non-Solana deposit chain to the user's Solana wallet.
type DepositSweep struct {
	ID          uuid.UUID        `db:"id"`
	DepositID   uuid.UUID        `db:"deposit_id"`
	UserID      uuid.UUID        `db:"user_id"`
	SourceChain string           `db:"source_chain"`
	Amount      decimal.Decimal  `db:"amount"`
	FeeAmount   *decimal.Decimal `db:"fee_amount"`
	// FundingAmount is the exact USDC (incl. bridge fees) to transfer to the
	// intent address, fixed at intent creation so retried funding attempts are
	// byte-identical (same amount, same per-intent Circle idempotency key).
	FundingAmount      *decimal.Decimal `db:"funding_amount"`
	IntentAddress      *string          `db:"intent_address"`
	ChainRailsIntentID *int             `db:"chainrails_intent_id"`
	Status             string           `db:"status"`
	TxHash             *string          `db:"tx_hash"`
	ErrorMessage       *string          `db:"error_message"`
	Attempts           int              `db:"attempts"`
	CreatedAt          time.Time        `db:"created_at"`
	UpdatedAt          time.Time        `db:"updated_at"`
	CompletedAt        *time.Time       `db:"completed_at"`
}
