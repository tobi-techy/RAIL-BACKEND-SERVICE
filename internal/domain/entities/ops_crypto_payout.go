package entities

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrOpsPayoutSuppressionConflict means the source transaction already has a
// deposit row, so it cannot be bridged off-ledger without a human decision.
// This is a deliberate fail-closed outcome: the row may already be credited, or
// may be a failed credit that needs inspection first.
var ErrOpsPayoutSuppressionConflict = errors.New("a deposit row already exists for this transaction")

// OpsCryptoPayout is one operator-driven, off-ledger bridge of crypto out of a
// Rail-managed wallet and into a destination wallet.
//
// It is not a withdrawal and not a deposit: no ledger account is debited or
// credited. The row exists so an operator can see exactly what was moved, why,
// who authorised it, and which provider objects (ChainRails intent, Circle
// transfer) carry the money.
//
// The same row is resumed across retries rather than recreated, so a crash
// between funding and confirmation cannot produce a second transfer.
type OpsCryptoPayout struct {
	ID     uuid.UUID `db:"id"`
	UserID uuid.UUID `db:"user_id"`

	// Source is the Rail-managed wallet the money leaves from.
	SourceChain          string     `db:"source_chain"`
	SourceWalletID       *uuid.UUID `db:"source_wallet_id"`
	SourceCircleWalletID string     `db:"source_circle_wallet_id"`
	SourceAddress        string     `db:"source_address"`

	// SourceCircleTxID is the inbound Circle transaction being settled. It is
	// also the idempotency anchor: at most one payout may ever reference it.
	SourceCircleTxID string `db:"source_circle_tx_id"`
	SourceTxHash     string `db:"source_tx_hash"`

	DestinationChain   string `db:"destination_chain"`
	DestinationAddress string `db:"destination_address"`

	// Amount is what the recipient receives. FundingAmount is what actually
	// leaves the source wallet: Amount plus ChainRails' bridge fees. FeeAmount
	// is the difference, retained for reporting.
	Amount        decimal.Decimal  `db:"amount"`
	FundingAmount *decimal.Decimal `db:"funding_amount"`
	FeeAmount     *decimal.Decimal `db:"fee_amount"`

	ChainRailsIntentID *int    `db:"chainrails_intent_id"`
	IntentAddress      *string `db:"intent_address"`
	CircleTransferID   *string `db:"circle_transfer_id"`
	CircleTxHash       *string `db:"circle_tx_hash"`

	Status string `db:"status"`

	// SuppressionDepositID points at the deposits row (status 'bridged') that
	// stops the normal credit path from paying the same money out twice.
	SuppressionDepositID *uuid.UUID `db:"suppression_deposit_id"`

	// Attempts counts resumed retries of this same payout.
	Attempts     int     `db:"attempts"`
	Reason       string  `db:"reason"`
	Actor        string  `db:"actor"`
	ErrorMessage *string `db:"error_message"`

	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	CompletedAt *time.Time `db:"completed_at"`
}

// Ops crypto payout statuses.
//
// The split that matters is whether money may already be in flight. Anything in
// the in-flight set keeps the suppression row; only `failed` and `refunded`
// release it, because only those prove nothing left the wallet.
const (
	// OpsPayoutStatusCreated: row and suppression written; no provider call yet.
	OpsPayoutStatusCreated = "created"
	// OpsPayoutStatusIntentCreating: one request has claimed the row to create
	// a ChainRails intent. Stale claims may be taken over after 5 minutes.
	OpsPayoutStatusIntentCreating = "intent_creating"
	// OpsPayoutStatusIntentCreated: intent persisted with its exact funding
	// amount; no transfer attempted yet.
	OpsPayoutStatusIntentCreated = "intent_created"
	// OpsPayoutStatusFunding: a Circle transfer has been initiated.
	OpsPayoutStatusFunding = "funding"
	// OpsPayoutStatusFunded: the Circle transfer completed; the intent is funded.
	OpsPayoutStatusFunded = "funded"
	// OpsPayoutStatusCompleted: ChainRails reports the intent settled on the
	// destination chain.
	OpsPayoutStatusCompleted = "completed"
	// OpsPayoutStatusFundingUncertain: the transfer call errored or did not
	// confirm in time. Money may have moved, so suppression must stay and a
	// retry must reuse the same idempotency key.
	OpsPayoutStatusFundingUncertain = "funding_uncertain"
	// OpsPayoutStatusFailed: a definitive pre-transfer failure. Suppression is
	// released so the normal deposit credit path still works.
	OpsPayoutStatusFailed = "failed"
	// OpsPayoutStatusRefunded: ChainRails returned the funds to the refund
	// address. Suppression is released.
	OpsPayoutStatusRefunded = "refunded"
)

// SuppressionHeld reports whether a payout may have moved money, and therefore
// whether the source transaction must stay blocked from ledger credit.
func (p *OpsCryptoPayout) SuppressionHeld() bool {
	switch p.Status {
	case OpsPayoutStatusFailed, OpsPayoutStatusRefunded:
		return false
	default:
		return true
	}
}
