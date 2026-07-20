package graph

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// VerifyWebhookSignature validates the HMAC-SHA256 signature Graph sends over
// the raw request body using the webhook signing secret.
func VerifyWebhookSignature(body []byte, signature, secret string) error {
	sig := strings.TrimSpace(signature)
	sig = strings.TrimPrefix(sig, "sha256=")
	sig = strings.ToLower(strings.TrimSpace(sig))
	if sig == "" {
		return fmt.Errorf("missing signature")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// WebhookEvent is the envelope Graph sends for all webhook events.
// The actual event kind is in EventType (e.g. "account.created", "account.credit").
type WebhookEvent struct {
	EventType string       `json:"event_type"`
	Entity    string       `json:"entity"` // "bank_account", "transaction", "card", "address"
	Data      WebhookData  `json:"data"`
	CreatedAt string       `json:"created_at,omitempty"`
}

// WebhookData carries the relevant fields across issuance and transaction events.
// For account events (entity=bank_account): full bank account details.
// For transaction events (entity=transaction): deposit/payout/conversion details.
// For card events (entity=card): card issuance/freeze/close details.
// For address events (entity=address): deposit address migration details.
type WebhookData struct {
	// -- Bank account fields (entity=bank_account) --
	ID            string  `json:"id"`
	HolderID      string  `json:"holder_id"`
	HolderType    string  `json:"holder_type"`
	Label         string  `json:"label"`
	AccountName   string  `json:"account_name"`
	AccountNumber string  `json:"account_number"`
	RoutingNumber string  `json:"routing_number"`
	BankName      string  `json:"bank_name"`
	BankCode      string  `json:"bank_code"`
	BankAddress   *Address `json:"bank_address,omitempty"`
	Currency      string  `json:"currency"`
	Balance       float64 `json:"balance"`
	CreditPending float64 `json:"credit_pending"`
	DebitPending  float64 `json:"debit_pending"`
	Type          string  `json:"type"`
	Status        string  `json:"status"`
	IsDeleted     bool    `json:"is_deleted"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`

	// -- Transaction fields (entity=transaction) --
	AccountID           string  `json:"account_id,omitempty"`
	Amount              float64 `json:"amount,omitempty"`
	BalanceBefore       float64 `json:"balance_before,omitempty"`
	BalanceAfter        float64 `json:"balance_after,omitempty"`
	Kind                string  `json:"kind,omitempty"`                // deposit, payout, conversion, charge
	TransactionType     string  `json:"transaction_type,omitempty"`    // credit | debit
	Description         string  `json:"description,omitempty"`
	DepositID           string  `json:"deposit_id,omitempty"`
	PayoutID            string  `json:"payout_id,omitempty"`
	ConversionID        string  `json:"conversion_id,omitempty"`
	LinkedTransactionID string  `json:"linked_transaction_id,omitempty"`
	CustomReference     string  `json:"custom_reference,omitempty"`

	// -- Card fields (entity=card) --
	CardID         string `json:"card_id,omitempty"`
	Brand          string `json:"brand,omitempty"`
	CardholderName string `json:"cardholder_name,omitempty"`
	NumberMasked   string `json:"number_masked,omitempty"`

	// -- Address fields (entity=address) --
	IsMaster    bool   `json:"is_master,omitempty"`
	Code        string `json:"code,omitempty"`
	Network     string `json:"network,omitempty"`

	// Nested deposit object (for account.credit events)
	Deposit *WebhookDeposit `json:"deposit,omitempty"`

	// Nested bank_account object (for transaction events)
	BankAccount *WebhookData `json:"bank_account,omitempty"`

	// Nested card object (for card.transaction events)
	Card *WebhookData `json:"card,omitempty"`

	// Migration metadata (for account.migrated, address.migrated events)
	MigrationMeta *MigrationMeta `json:"migration_meta,omitempty"`
}

// MigrationMeta carries details about account/address migration events.
type MigrationMeta struct {
	Previous       string  `json:"previous,omitempty"`
	Next           string  `json:"next,omitempty"`
	BalanceBrought float64 `json:"balance_brought,omitempty"`
	BalanceCarried float64 `json:"balance_carried,omitempty"`
	BalanceRefunded float64 `json:"balance_refunded,omitempty"`
}

// WebhookDeposit is the nested deposit object in transaction/account.credit events.
type WebhookDeposit struct {
	ID            string  `json:"id"`
	AccountID     string  `json:"account_id"`
	Amount        float64 `json:"amount"`
	AmountSettled float64 `json:"amount_settled"`
	Currency      string  `json:"currency"`
	Fee           float64 `json:"fee"`
	Status        string  `json:"status"`
	Type          string  `json:"type"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}
