package blend

import (
	"database/sql"
	"math/big"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// depositRoute mirrors a row in blend_deposit_routes.
type depositRoute struct {
	ID             uuid.UUID       `db:"id"`
	DepositID      uuid.NullUUID   `db:"deposit_id"`
	UserID         uuid.UUID       `db:"user_id"`
	BlendAccountID string          `db:"blend_account_id"`
	EOAAddress     string          `db:"eoa_address"`
	SafeAddress    string          `db:"safe_address"`
	CircleWalletID string          `db:"circle_wallet_id"`
	ChainID        int64           `db:"chain_id"`
	InputAsset     string          `db:"input_asset"`
	Amount         decimal.Decimal `db:"amount"`
	AmountUnits    sql.NullString  `db:"amount_units"`

	SourceCircleWalletID sql.NullString      `db:"source_circle_wallet_id"`
	SourceChain          sql.NullString      `db:"source_chain"`
	BridgeIntentID       sql.NullInt64       `db:"bridge_intent_id"`
	BridgeIntentAddress  sql.NullString      `db:"bridge_intent_address"`
	BridgeSourceChain    sql.NullString      `db:"bridge_source_chain"`
	BridgeFundAmount     decimal.NullDecimal `db:"bridge_fund_amount"`
	BridgeTxHash         sql.NullString      `db:"bridge_tx_hash"`
	FundedAt             sql.NullTime        `db:"funded_at"`

	IntentID        sql.NullString `db:"intent_id"`
	IntentStatus    sql.NullString `db:"intent_status"`
	IntentExpiresAt sql.NullTime   `db:"intent_expires_at"`
	QuotePayload    []byte         `db:"quote_payload"`
	QuoteSummary    []byte         `db:"quote_summary"`
	TxHash          sql.NullString `db:"tx_hash"`
	SubmittedAt     sql.NullTime   `db:"submitted_at"`
	SettledAt       sql.NullTime   `db:"settled_at"`
	Status          string         `db:"status"`
	Attempts        int            `db:"attempts"`
	LastError       sql.NullString `db:"last_error"`
	NextRetryAt     sql.NullTime   `db:"next_retry_at"`
	ExternalRef     sql.NullString `db:"external_ref"`
	Source          string         `db:"source"`
}

func nullUUIDString(n uuid.NullUUID) string {
	if !n.Valid {
		return ""
	}
	return n.UUID.String()
}

const depositRouteSelect = `
	SELECT id, deposit_id, user_id, blend_account_id, eoa_address,
		COALESCE(safe_address, '') AS safe_address, circle_wallet_id, chain_id,
		input_asset, amount, amount_units,
		source_circle_wallet_id, source_chain, bridge_intent_id, bridge_intent_address,
		bridge_source_chain, bridge_fund_amount, bridge_tx_hash, funded_at,
		intent_id, intent_status, intent_expires_at,
		quote_payload, quote_summary, tx_hash, submitted_at, settled_at,
		status, attempts, last_error, next_retry_at, external_ref, source
	FROM blend_deposit_routes`

// blendUserAccount mirrors a row in blend_user_accounts.
type blendUserAccount struct {
	ID              uuid.UUID    `db:"id"`
	UserID          uuid.UUID    `db:"user_id"`
	EOAAddress      string       `db:"eoa_address"`
	BlendAccountID  string       `db:"blend_account_id"`
	SafeAddress     string       `db:"safe_address"`
	ChainID         int64        `db:"chain_id"`
	SafeStatus      string       `db:"safe_status"`
	SafeRequestedAt sql.NullTime `db:"safe_requested_at"`
	SafeDeployedAt  sql.NullTime `db:"safe_deployed_at"`
	CircleWalletID  string       `db:"circle_wallet_id"`
}

// redemption mirrors a row in blend_yield_redemptions.
type redemption struct {
	ID                 uuid.UUID           `db:"id"`
	UserID             uuid.UUID           `db:"user_id"`
	BlendAccountID     string              `db:"blend_account_id"`
	Amount             decimal.Decimal     `db:"amount"`
	DestinationChainID int64               `db:"destination_chain_id"`
	IntentID           sql.NullString      `db:"intent_id"`
	IntentStatus       sql.NullString      `db:"intent_status"`
	QuotePayload       []byte              `db:"quote_payload"`
	TxHash             sql.NullString      `db:"tx_hash"`
	SubmittedAt        sql.NullTime        `db:"submitted_at"`
	SettledAt          sql.NullTime        `db:"settled_at"`
	IdempotencyKey     string              `db:"idempotency_key"`
	Status             string              `db:"status"`
	Attempts           int                 `db:"attempts"`
	LastError          sql.NullString      `db:"last_error"`
	NextRetryAt        sql.NullTime        `db:"next_retry_at"`
	PreRedeemBalance   decimal.NullDecimal `db:"pre_redeem_eoa_balance"`
}

func parseBig(s string) (*big.Int, bool) {
	s = trimSpace(s)
	if s == "" {
		return nil, false
	}
	return new(big.Int).SetString(s, 10)
}

func trimSpace(s string) string {
	// local helper to avoid importing strings just for one call site that
	// also needs to treat NULL string ("") uniformly.
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ' ' || last == '\t' || last == '\n' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}
