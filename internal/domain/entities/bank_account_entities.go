package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BankAccountStatus represents the verification status of a bank account
type BankAccountStatus string

const (
	BankAccountStatusPending  BankAccountStatus = "pending"
	BankAccountStatusVerified BankAccountStatus = "verified"
	BankAccountStatusFailed   BankAccountStatus = "failed"
)

// ValidBankAccountStatuses contains all valid bank account statuses
var ValidBankAccountStatuses = map[BankAccountStatus]bool{
	BankAccountStatusPending:  true,
	BankAccountStatusVerified: true,
	BankAccountStatusFailed:   true,
}

// BankAccountCurrency represents supported bank account currencies
type BankAccountCurrency string

const (
	BankAccountCurrencyUSD BankAccountCurrency = "USD"
	BankAccountCurrencyEUR BankAccountCurrency = "EUR"
)

// ValidBankAccountCurrencies contains all valid currencies
var ValidBankAccountCurrencies = map[BankAccountCurrency]bool{
	BankAccountCurrencyUSD: true,
	BankAccountCurrencyEUR: true,
}

// BankAccount represents a user's linked bank account
type BankAccount struct {
	ID                 uuid.UUID           `json:"id" db:"id"`
	UserID             uuid.UUID           `json:"user_id" db:"user_id"`
	BankName           string              `json:"bank_name" db:"bank_name"`
	AccountNumberLast4 string              `json:"account_number_last4" db:"account_number_last4"`
	RoutingNumber      *string             `json:"-" db:"routing_number"` // Full routing number, not exposed in JSON
	RoutingNumberLast4 *string             `json:"routing_number_last4,omitempty" db:"routing_number_last4"`
	IBAN               *string             `json:"iban,omitempty" db:"iban"`
	BIC                *string             `json:"bic,omitempty" db:"bic"`
	Currency           BankAccountCurrency `json:"currency" db:"currency"`
	IsVerified         bool                `json:"is_verified" db:"is_verified"`
	IsPrimary          bool                `json:"is_primary" db:"is_primary"`
	BridgeRecipientID  *string             `json:"bridge_recipient_id,omitempty" db:"bridge_recipient_id"`
	CreatedAt          time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at" db:"updated_at"`
}

// Validate validates the bank account entity
func (b *BankAccount) Validate() error {
	if b.ID == uuid.Nil {
		return fmt.Errorf("bank account ID is required")
	}
	if b.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if b.BankName == "" {
		return fmt.Errorf("bank name is required")
	}
	if b.AccountNumberLast4 == "" {
		return fmt.Errorf("account number last 4 is required")
	}
	if len(b.AccountNumberLast4) != 4 {
		return fmt.Errorf("account number last 4 must be exactly 4 characters")
	}
	if b.Currency != BankAccountCurrencyUSD && b.Currency != BankAccountCurrencyEUR {
		return fmt.Errorf("invalid currency: %s", b.Currency)
	}
	// For USD, require routing number
	if b.Currency == BankAccountCurrencyUSD && b.RoutingNumberLast4 == nil {
		return fmt.Errorf("routing number is required for USD accounts")
	}
	// For EUR, require IBAN
	if b.Currency == BankAccountCurrencyEUR && b.IBAN == nil {
		return fmt.Errorf("IBAN is required for EUR accounts")
	}
	return nil
}

// CanReceiveWithdrawal checks if this bank account can receive a withdrawal of the given currency
func (b *BankAccount) CanReceiveWithdrawal(currency WithdrawalCurrency) bool {
	if !b.IsVerified {
		return false
	}
	switch currency {
	case WithdrawalCurrencyUSD:
		return b.Currency == BankAccountCurrencyUSD
	case WithdrawalCurrencyEUR:
		return b.Currency == BankAccountCurrencyEUR
	default:
		return false
	}
}

// AddBankAccountRequest represents a request to add a new bank account
type AddBankAccountRequest struct {
	UserID            uuid.UUID           `json:"user_id"`
	BankName          string              `json:"bank_name"`
	AccountNumber     string              `json:"account_number"`
	RoutingNumber     *string             `json:"routing_number,omitempty"`
	IBAN              *string             `json:"iban,omitempty"`
	BIC               *string             `json:"bic,omitempty"`
	Currency          BankAccountCurrency `json:"currency"`
	IsPrimary         bool                `json:"is_primary"`
}

// Validate validates the add bank account request
func (r *AddBankAccountRequest) Validate() error {
	if r.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if r.BankName == "" {
		return fmt.Errorf("bank name is required")
	}
	if r.AccountNumber == "" {
		return fmt.Errorf("account number is required")
	}
	if len(r.AccountNumber) < 4 {
		return fmt.Errorf("account number must be at least 4 characters")
	}
	if r.Currency != BankAccountCurrencyUSD && r.Currency != BankAccountCurrencyEUR {
		return fmt.Errorf("invalid currency: %s", r.Currency)
	}
	if r.Currency == BankAccountCurrencyUSD && (r.RoutingNumber == nil || *r.RoutingNumber == "") {
		return fmt.Errorf("routing number is required for USD accounts")
	}
	if r.Currency == BankAccountCurrencyEUR && (r.IBAN == nil || *r.IBAN == "") {
		return fmt.Errorf("IBAN is required for EUR accounts")
	}
	return nil
}

// GetAccountNumberLast4 returns the last 4 digits of the account number
func (r *AddBankAccountRequest) GetAccountNumberLast4() string {
	if len(r.AccountNumber) >= 4 {
		return r.AccountNumber[len(r.AccountNumber)-4:]
	}
	return r.AccountNumber
}

// BankAccountResponse represents the response for bank account operations
type BankAccountResponse struct {
	BankAccount BankAccount `json:"bank_account"`
	Message     string     `json:"message,omitempty"`
}

// ListBankAccountsResponse represents the response for listing bank accounts
type ListBankAccountsResponse struct {
	BankAccounts []BankAccount `json:"bank_accounts"`
	TotalCount   int           `json:"total_count"`
}

// BankAccountVerificationRequest represents a request to verify a bank account
type BankAccountVerificationRequest struct {
	UserID        uuid.UUID `json:"user_id"`
	BankAccountID uuid.UUID `json:"bank_account_id"`
	DepositAmount1 decimal.Decimal `json:"deposit_amount_1"`
	DepositAmount2 decimal.Decimal `json:"deposit_amount_2"`
}
