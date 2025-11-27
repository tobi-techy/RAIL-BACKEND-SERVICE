package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AccountType represents the type of ledger account
type AccountType string

const (
	// User account types
	AccountTypeUSDCBalance       AccountType = "usdc_balance"       // User's available USDC (legacy, pre-allocation mode)
	AccountTypeFiatExposure      AccountType = "fiat_exposure"      // User's buying power at Alpaca (USD)
	AccountTypePendingInvestment AccountType = "pending_investment" // User's reserved funds for in-flight trades

	// Smart Allocation Mode account types
	AccountTypeSpendingBalance AccountType = "spending_balance" // User's 70% spending balance (available for payments)
	AccountTypeStashBalance    AccountType = "stash_balance"    // User's 30% stash balance (locked savings)

	// System account types
	AccountTypeSystemBufferUSDC  AccountType = "system_buffer_usdc" // System on-chain USDC reserve
	AccountTypeSystemBufferFiat  AccountType = "system_buffer_fiat" // System operational USD buffer
	AccountTypeBrokerOperational AccountType = "broker_operational" // Pre-funded cash at Alpaca
)

// IsUserAccountType returns true if the account type belongs to a user
func (a AccountType) IsUserAccountType() bool {
	return a == AccountTypeUSDCBalance ||
		a == AccountTypeFiatExposure ||
		a == AccountTypePendingInvestment ||
		a == AccountTypeSpendingBalance ||
		a == AccountTypeStashBalance
}

// IsSystemAccountType returns true if the account type is system-level
func (a AccountType) IsSystemAccountType() bool {
	return a == AccountTypeSystemBufferUSDC ||
		a == AccountTypeSystemBufferFiat ||
		a == AccountTypeBrokerOperational
}

// IsSystemAccount is an alias for IsSystemAccountType
func (a AccountType) IsSystemAccount() bool {
	return a.IsSystemAccountType()
}

// IsValid checks if the account type is valid
func (a AccountType) IsValid() bool {
	return a.Validate() == nil
}

// Validate checks if the account type is valid
func (a AccountType) Validate() error {
	switch a {
	case AccountTypeUSDCBalance, AccountTypeFiatExposure, AccountTypePendingInvestment,
		AccountTypeSpendingBalance, AccountTypeStashBalance,
		AccountTypeSystemBufferUSDC, AccountTypeSystemBufferFiat, AccountTypeBrokerOperational:
		return nil
	default:
		return fmt.Errorf("invalid account type: %s", a)
	}
}

// TransactionType represents the type of ledger transaction
type TransactionType string

const (
	TransactionTypeDeposit             TransactionType = "deposit"
	TransactionTypeWithdrawal          TransactionType = "withdrawal"
	TransactionTypeInvestment          TransactionType = "investment"
	TransactionTypeConversion          TransactionType = "conversion"
	TransactionTypeInternalTransfer    TransactionType = "internal_transfer"
	TransactionTypeBufferReplenishment TransactionType = "buffer_replenishment"
	TransactionTypeReversal            TransactionType = "reversal"
)

// Validate checks if the transaction type is valid
func (t TransactionType) Validate() error {
	switch t {
	case TransactionTypeDeposit, TransactionTypeWithdrawal, TransactionTypeInvestment,
		TransactionTypeConversion, TransactionTypeInternalTransfer,
		TransactionTypeBufferReplenishment, TransactionTypeReversal:
		return nil
	default:
		return fmt.Errorf("invalid transaction type: %s", t)
	}
}

// TransactionStatus represents the status of a ledger transaction
type TransactionStatus string

const (
	TransactionStatusPending   TransactionStatus = "pending"
	TransactionStatusCompleted TransactionStatus = "completed"
	TransactionStatusReversed  TransactionStatus = "reversed"
	TransactionStatusFailed    TransactionStatus = "failed"
)

// Validate checks if the transaction status is valid
func (s TransactionStatus) Validate() error {
	switch s {
	case TransactionStatusPending, TransactionStatusCompleted, TransactionStatusReversed, TransactionStatusFailed:
		return nil
	default:
		return fmt.Errorf("invalid transaction status: %s", s)
	}
}

// EntryType represents debit or credit
type EntryType string

const (
	EntryTypeDebit  EntryType = "debit"
	EntryTypeCredit EntryType = "credit"
)

// Validate checks if the entry type is valid
func (e EntryType) Validate() error {
	switch e {
	case EntryTypeDebit, EntryTypeCredit:
		return nil
	default:
		return fmt.Errorf("invalid entry type: %s", e)
	}
}

// LedgerAccount represents a financial account in the double-entry system
type LedgerAccount struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      *uuid.UUID      `json:"user_id,omitempty" db:"user_id"`
	AccountType AccountType     `json:"account_type" db:"account_type"`
	Currency    string          `json:"currency" db:"currency"`
	Balance     decimal.Decimal `json:"balance" db:"balance"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate validates the ledger account
func (a *LedgerAccount) Validate() error {
	if a.ID == uuid.Nil {
		return fmt.Errorf("account ID is required")
	}

	if err := a.AccountType.Validate(); err != nil {
		return err
	}

	// User accounts must have a user_id
	if a.AccountType.IsUserAccountType() && a.UserID == nil {
		return fmt.Errorf("user account requires user_id")
	}

	// System accounts must not have a user_id
	if a.AccountType.IsSystemAccountType() && a.UserID != nil {
		return fmt.Errorf("system account cannot have user_id")
	}

	if a.Currency != "USDC" && a.Currency != "USD" {
		return fmt.Errorf("invalid currency: %s", a.Currency)
	}

	if a.Balance.IsNegative() {
		return fmt.Errorf("account balance cannot be negative")
	}

	return nil
}

// IsSystemAccount returns true if this is a system-level account
func (a *LedgerAccount) IsSystemAccount() bool {
	return a.UserID == nil
}

// LedgerTransaction represents a group of balanced ledger entries
type LedgerTransaction struct {
	ID              uuid.UUID         `json:"id" db:"id"`
	UserID          *uuid.UUID        `json:"user_id,omitempty" db:"user_id"`
	TransactionType TransactionType   `json:"transaction_type" db:"transaction_type"`
	ReferenceID     *uuid.UUID        `json:"reference_id,omitempty" db:"reference_id"`
	ReferenceType   *string           `json:"reference_type,omitempty" db:"reference_type"`
	Status          TransactionStatus `json:"status" db:"status"`
	IdempotencyKey  string            `json:"idempotency_key" db:"idempotency_key"`
	Description     *string           `json:"description,omitempty" db:"description"`
	Metadata        map[string]any    `json:"metadata,omitempty" db:"metadata"`
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty" db:"completed_at"`
}

// Validate validates the ledger transaction
func (t *LedgerTransaction) Validate() error {
	if t.ID == uuid.Nil {
		return fmt.Errorf("transaction ID is required")
	}

	if err := t.TransactionType.Validate(); err != nil {
		return err
	}

	if err := t.Status.Validate(); err != nil {
		return err
	}

	if t.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}

	return nil
}

// MarkCompleted marks the transaction as completed
func (t *LedgerTransaction) MarkCompleted() {
	now := time.Now()
	t.Status = TransactionStatusCompleted
	t.CompletedAt = &now
}

// MarkFailed marks the transaction as failed
func (t *LedgerTransaction) MarkFailed() {
	t.Status = TransactionStatusFailed
}

// LedgerEntry represents an individual debit or credit entry
type LedgerEntry struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	TransactionID uuid.UUID       `json:"transaction_id" db:"transaction_id"`
	AccountID     uuid.UUID       `json:"account_id" db:"account_id"`
	EntryType     EntryType       `json:"entry_type" db:"entry_type"`
	Amount        decimal.Decimal `json:"amount" db:"amount"`
	Currency      string          `json:"currency" db:"currency"`
	Description   *string         `json:"description,omitempty" db:"description"`
	Metadata      map[string]any  `json:"metadata,omitempty" db:"metadata"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// Validate validates the ledger entry
func (e *LedgerEntry) Validate() error {
	if e.ID == uuid.Nil {
		return fmt.Errorf("entry ID is required")
	}

	if e.TransactionID == uuid.Nil {
		return fmt.Errorf("transaction ID is required")
	}

	if e.AccountID == uuid.Nil {
		return fmt.Errorf("account ID is required")
	}

	if err := e.EntryType.Validate(); err != nil {
		return err
	}

	if e.Amount.IsNegative() {
		return fmt.Errorf("entry amount cannot be negative")
	}

	if e.Amount.IsZero() {
		return fmt.Errorf("entry amount cannot be zero")
	}

	if e.Currency != "USDC" && e.Currency != "USD" {
		return fmt.Errorf("invalid currency: %s", e.Currency)
	}

	return nil
}

// IsDebit returns true if this is a debit entry
func (e *LedgerEntry) IsDebit() bool {
	return e.EntryType == EntryTypeDebit
}

// IsCredit returns true if this is a credit entry
func (e *LedgerEntry) IsCredit() bool {
	return e.EntryType == EntryTypeCredit
}

// UserBalances represents all balance accounts for a user
type UserBalances struct {
	UserID             uuid.UUID       `json:"user_id"`
	USDCBalance        decimal.Decimal `json:"usdc_balance"`
	FiatExposure       decimal.Decimal `json:"fiat_exposure"`
	PendingInvestment  decimal.Decimal `json:"pending_investment"`
	TotalUSDEquivalent decimal.Decimal `json:"total_usd_equivalent"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func (b *UserBalances) TotalValue() decimal.Decimal {
	return b.CalculateTotalUSD()
}

// CalculateTotalUSD calculates total balance in USD equivalent
// Assumes 1 USDC = 1 USD for simplicity
func (b *UserBalances) CalculateTotalUSD() decimal.Decimal {
	return b.USDCBalance.Add(b.FiatExposure).Add(b.PendingInvestment)
}

// SystemBuffers represents the operational buffer balances
type SystemBuffers struct {
	BufferUSDC        decimal.Decimal `json:"buffer_usdc"`
	BufferFiat        decimal.Decimal `json:"buffer_fiat"`
	BrokerOperational decimal.Decimal `json:"broker_operational"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// CreateTransactionRequest represents a request to create a ledger transaction
type CreateTransactionRequest struct {
	UserID          *uuid.UUID
	TransactionType TransactionType
	ReferenceID     *uuid.UUID
	ReferenceType   *string
	IdempotencyKey  string
	Description     *string
	Metadata        map[string]any
	Entries         []CreateEntryRequest
}

// Validate validates the create transaction request
func (r *CreateTransactionRequest) Validate() error {
	if err := r.TransactionType.Validate(); err != nil {
		return err
	}

	if r.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}

	if len(r.Entries) < 2 {
		return fmt.Errorf("transaction must have at least 2 entries")
	}

	// Validate each entry
	for i, entry := range r.Entries {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
	}

	// Validate double-entry balance
	var debitSum, creditSum decimal.Decimal
	for _, entry := range r.Entries {
		if entry.EntryType == EntryTypeDebit {
			debitSum = debitSum.Add(entry.Amount)
		} else {
			creditSum = creditSum.Add(entry.Amount)
		}
	}

	if !debitSum.Equal(creditSum) {
		return fmt.Errorf("transaction is unbalanced: debits=%s, credits=%s", debitSum.String(), creditSum.String())
	}

	return nil
}

// CreateEntryRequest represents a request to create a ledger entry
type CreateEntryRequest struct {
	AccountID   uuid.UUID
	EntryType   EntryType
	Amount      decimal.Decimal
	Currency    string
	Description *string
	Metadata    map[string]any
}

// Validate validates the create entry request
func (r *CreateEntryRequest) Validate() error {
	if r.AccountID == uuid.Nil {
		return fmt.Errorf("account ID is required")
	}

	if err := r.EntryType.Validate(); err != nil {
		return err
	}

	if r.Amount.IsNegative() {
		return fmt.Errorf("entry amount cannot be negative")
	}

	if r.Amount.IsZero() {
		return fmt.Errorf("entry amount cannot be zero")
	}

	if r.Currency != "USDC" && r.Currency != "USD" {
		return fmt.Errorf("invalid currency: %s", r.Currency)
	}

	return nil
}
