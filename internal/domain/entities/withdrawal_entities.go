package entities

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WithdrawalType represents the type of withdrawal
type WithdrawalType string

const (
	WithdrawalTypeCrypto WithdrawalType = "crypto" // USDC to external crypto wallet
	WithdrawalTypeFiat   WithdrawalType = "fiat"   // USDC to fiat via Bridge offramp
)

// ValidWithdrawalTypes contains all valid withdrawal types
var ValidWithdrawalTypes = map[WithdrawalType]bool{
	WithdrawalTypeCrypto: true,
	WithdrawalTypeFiat:   true,
}

// WithdrawalCurrency represents the withdrawal currency
type WithdrawalCurrency string

const (
	WithdrawalCurrencyUSDC  WithdrawalCurrency = "USDC"
	WithdrawalCurrencyUSDT  WithdrawalCurrency = "USDT"
	WithdrawalCurrencyEURC  WithdrawalCurrency = "EURC"
	WithdrawalCurrencyPYUSD WithdrawalCurrency = "PYUSD"
	WithdrawalCurrencyUSDG  WithdrawalCurrency = "USDG"
	WithdrawalCurrencyUSD   WithdrawalCurrency = "USD"
	WithdrawalCurrencyEUR   WithdrawalCurrency = "EUR"
	WithdrawalCurrencyGBP   WithdrawalCurrency = "GBP"
	WithdrawalCurrencyNGN   WithdrawalCurrency = "NGN"
)

// ValidWithdrawalCurrencies contains all valid withdrawal currencies
var ValidWithdrawalCurrencies = map[WithdrawalCurrency]bool{
	WithdrawalCurrencyUSDC:  true,
	WithdrawalCurrencyUSDT:  true,
	WithdrawalCurrencyEURC:  true,
	WithdrawalCurrencyPYUSD: true,
	WithdrawalCurrencyUSDG:  true,
	WithdrawalCurrencyUSD:   true,
	WithdrawalCurrencyEUR:   true,
	WithdrawalCurrencyGBP:   true,
	WithdrawalCurrencyNGN:   true,
}

// WithdrawalSourceAccount represents the source account for withdrawal
type WithdrawalSourceAccount string

const (
	WithdrawalSourceSpendingBalance WithdrawalSourceAccount = "spending_balance"
	WithdrawalSourceStashBalance    WithdrawalSourceAccount = "stash_balance"
)

// ValidWithdrawalSourceAccounts contains all valid source accounts
var ValidWithdrawalSourceAccounts = map[WithdrawalSourceAccount]bool{
	WithdrawalSourceSpendingBalance: true,
	WithdrawalSourceStashBalance:    true,
}

// DestinationType represents the destination type for withdrawal
type DestinationType string

const (
	DestinationTypeCryptoWallet DestinationType = "crypto_wallet"
	DestinationTypeBankAccount  DestinationType = "bank_account"
)

// ValidDestinationTypes contains all valid destination types
var ValidDestinationTypes = map[DestinationType]bool{
	DestinationTypeCryptoWallet: true,
	DestinationTypeBankAccount:  true,
}

// WithdrawalStatus represents the status of a withdrawal
type WithdrawalStatus string

const (
	WithdrawalStatusInitiated            WithdrawalStatus = "initiated"             // Request created
	WithdrawalStatusPending              WithdrawalStatus = "pending"               // Sent to processor
	WithdrawalStatusProcessing           WithdrawalStatus = "processing"            // Provider processing
	WithdrawalStatusAwaitingConfirmation WithdrawalStatus = "awaiting_confirmation" // Waiting for on-chain/fiat confirmation
	WithdrawalStatusOnChainTransfer      WithdrawalStatus = "onchain_transfer"      // On-chain transfer in progress
	WithdrawalStatusTimeout              WithdrawalStatus = "timeout"               // No response within SLA
	WithdrawalStatusCompleted            WithdrawalStatus = "completed"             // Terminal: success
	WithdrawalStatusFailed               WithdrawalStatus = "failed"                // Terminal: failed
	WithdrawalStatusReversed             WithdrawalStatus = "reversed"              // Terminal: reversed/refunded
	WithdrawalStatusCancelled            WithdrawalStatus = "cancelled"             // Terminal: cancelled by user
)

// ValidWithdrawalStatuses contains all valid withdrawal statuses
var ValidWithdrawalStatuses = map[WithdrawalStatus]bool{
	WithdrawalStatusInitiated:            true,
	WithdrawalStatusPending:              true,
	WithdrawalStatusProcessing:           true,
	WithdrawalStatusAwaitingConfirmation: true,
	WithdrawalStatusOnChainTransfer:      true,
	WithdrawalStatusTimeout:              true,
	WithdrawalStatusCompleted:            true,
	WithdrawalStatusFailed:               true,
	WithdrawalStatusReversed:             true,
	WithdrawalStatusCancelled:            true,
}

// ValidWithdrawalTransitions defines allowed status transitions
var ValidWithdrawalTransitions = map[WithdrawalStatus][]WithdrawalStatus{
	WithdrawalStatusInitiated:            {WithdrawalStatusPending, WithdrawalStatusProcessing, WithdrawalStatusFailed, WithdrawalStatusCancelled},
	WithdrawalStatusPending:              {WithdrawalStatusProcessing, WithdrawalStatusFailed, WithdrawalStatusTimeout, WithdrawalStatusCancelled},
	WithdrawalStatusProcessing:           {WithdrawalStatusAwaitingConfirmation, WithdrawalStatusFailed, WithdrawalStatusReversed, WithdrawalStatusCancelled},
	WithdrawalStatusAwaitingConfirmation: {WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusTimeout},
	WithdrawalStatusOnChainTransfer:      {WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusTimeout},
	WithdrawalStatusTimeout:              {WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusReversed},
	WithdrawalStatusCompleted:            {},
	WithdrawalStatusFailed:               {WithdrawalStatusReversed},
	WithdrawalStatusReversed:             {},
	WithdrawalStatusCancelled:            {},
}

// IsValid checks if the status is valid
func (s WithdrawalStatus) IsValid() bool {
	return ValidWithdrawalStatuses[s]
}

// CanTransitionTo checks if transition to new status is allowed
func (s WithdrawalStatus) CanTransitionTo(newStatus WithdrawalStatus) bool {
	allowed, exists := ValidWithdrawalTransitions[s]
	if !exists {
		return false
	}
	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}
	return false
}

// IsTerminal returns true if this is a terminal state
func (s WithdrawalStatus) IsTerminal() bool {
	return s == WithdrawalStatusCompleted || s == WithdrawalStatusFailed ||
		s == WithdrawalStatusReversed || s == WithdrawalStatusCancelled
}

// IsPending returns true if withdrawal is still in progress
func (s WithdrawalStatus) IsPending() bool {
	return !s.IsTerminal() && s != WithdrawalStatusTimeout
}

// ValidateTransition validates and returns error if transition is invalid
func (s WithdrawalStatus) ValidateTransition(newStatus WithdrawalStatus) error {
	if !newStatus.IsValid() {
		return fmt.Errorf("invalid withdrawal status: %s", newStatus)
	}
	if !s.CanTransitionTo(newStatus) {
		return fmt.Errorf("invalid status transition from %s to %s", s, newStatus)
	}
	return nil
}

// Withdrawal represents a withdrawal request
type Withdrawal struct {
	ID                 uuid.UUID               `json:"id" db:"id"`
	UserID             uuid.UUID               `json:"user_id" db:"user_id"`
	WithdrawalType     WithdrawalType          `json:"withdrawal_type" db:"withdrawal_type"`
	Currency           WithdrawalCurrency      `json:"currency" db:"currency"`
	Amount             decimal.Decimal         `json:"amount" db:"amount"`
	SourceAccount      WithdrawalSourceAccount `json:"source_account" db:"source_account"`
	BridgeWalletID     *string                 `json:"bridge_wallet_id,omitempty" db:"bridge_wallet_id"`
	DestinationType    DestinationType         `json:"destination_type" db:"destination_type"`
	DestinationChain   string                  `json:"destination_chain" db:"destination_chain"`
	DestinationAddress *string                 `json:"destination_address,omitempty" db:"destination_address"`
	BankAccountID      *uuid.UUID              `json:"bank_account_id,omitempty" db:"bank_account_id"`
	FeeAmount          decimal.Decimal         `json:"fee_amount" db:"fee_amount"`
	FeeCurrency        WithdrawalCurrency      `json:"fee_currency" db:"fee_currency"`
	Category           *string                 `json:"category,omitempty" db:"category"`
	Narration          *string                 `json:"narration,omitempty" db:"narration"`
	Status             WithdrawalStatus        `json:"status" db:"status"`
	ProviderTransferID *string                 `json:"provider_transfer_id,omitempty" db:"bridge_transfer_id"`
	TxHash             *string                 `json:"tx_hash,omitempty" db:"tx_hash"`
	ErrorMessage       *string                 `json:"error_message,omitempty" db:"error_message"`
	IdempotencyKey     *string                 `json:"idempotency_key,omitempty" db:"idempotency_key"`
	CreatedAt          time.Time               `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at" db:"updated_at"`
	CompletedAt        *time.Time              `json:"completed_at,omitempty" db:"completed_at"`
}

// Validate validates the withdrawal entity
func (w *Withdrawal) Validate() error {
	if w.ID == uuid.Nil {
		return fmt.Errorf("withdrawal ID is required")
	}
	if w.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if !ValidWithdrawalTypes[w.WithdrawalType] {
		return fmt.Errorf("invalid withdrawal type: %s", w.WithdrawalType)
	}
	if !ValidWithdrawalCurrencies[w.Currency] {
		return fmt.Errorf("invalid currency: %s", w.Currency)
	}
	if !ValidWithdrawalSourceAccounts[w.SourceAccount] {
		return fmt.Errorf("invalid source account: %s", w.SourceAccount)
	}
	if !ValidDestinationTypes[w.DestinationType] {
		return fmt.Errorf("invalid destination type: %s", w.DestinationType)
	}
	if w.Amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	if w.FeeAmount.IsNegative() {
		return fmt.Errorf("fee amount cannot be negative")
	}
	if w.FeeCurrency != "" && !ValidWithdrawalCurrencies[w.FeeCurrency] {
		return fmt.Errorf("invalid fee currency: %s", w.FeeCurrency)
	}
	return nil
}

// IsCrypto returns true if this is a crypto withdrawal
func (w *Withdrawal) IsCrypto() bool {
	return w.WithdrawalType == WithdrawalTypeCrypto
}

// IsFiat returns true if this is a fiat withdrawal
func (w *Withdrawal) IsFiat() bool {
	return w.WithdrawalType == WithdrawalTypeFiat
}

// InitiateCryptoWithdrawalRequest represents a crypto withdrawal request
type InitiateCryptoWithdrawalRequest struct {
	UserID             uuid.UUID               `json:"user_id"`
	Amount             decimal.Decimal         `json:"amount"`
	Currency           WithdrawalCurrency      `json:"currency"`            // USDC, USDT, EURC, PYUSD, USDG
	DestinationAddress string                  `json:"destination_address"`
	DestinationChain   string                  `json:"destination_chain"`
	SourceChain        string                  `json:"source_chain"`
	SourceAccount      WithdrawalSourceAccount `json:"source_account"`
	BridgeWalletID     string                  `json:"bridge_wallet_id"`
	CircleWalletID     string                  `json:"circle_wallet_id"`
	SourceWalletAddress string                 `json:"source_wallet_address"` // On-chain address for refunds
	Category           string                  `json:"category,omitempty"`
	Narration          string                  `json:"narration,omitempty"`
	Emergency          bool                    `json:"emergency"` // If true, bypass stash lock with penalty fee
	IdempotencyKey     string                  // Generated server-side
}

// Validate validates the crypto withdrawal request
func (r *InitiateCryptoWithdrawalRequest) Validate() error {
	if r.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if r.Amount.LessThan(MinWithdrawalAmount) {
		return fmt.Errorf("amount must be at least %s", MinWithdrawalAmount.String())
	}
	if r.Amount.GreaterThan(MaxWithdrawalAmount) {
		return fmt.Errorf("amount must be at most %s per transaction", MaxWithdrawalAmount.String())
	}
	if r.DestinationAddress == "" {
		return fmt.Errorf("destination address is required")
	}
	if r.DestinationChain == "" {
		return fmt.Errorf("destination chain is required")
	}
	if !ValidWithdrawalSourceAccounts[r.SourceAccount] {
		return fmt.Errorf("invalid source account: %s", r.SourceAccount)
	}
	if r.BridgeWalletID == "" && r.CircleWalletID == "" {
		return fmt.Errorf("wallet provider ID is required")
	}
	if r.Currency == "" || !r.Currency.IsStablecoinCurrency() {
		return fmt.Errorf("invalid crypto withdrawal currency: %s", r.Currency)
	}
	return nil
}

// InitiateFiatWithdrawalRequest represents a fiat withdrawal request
type InitiateFiatWithdrawalRequest struct {
	UserID            uuid.UUID               `json:"user_id"`
	Amount            decimal.Decimal         `json:"amount"`
	Currency          WithdrawalCurrency      `json:"currency"`
	AccountHolderName string                  `json:"account_holder_name"`
	AccountNumber     string                  `json:"account_number,omitempty"` // USD only
	RoutingNumber     string                  `json:"routing_number,omitempty"` // USD only
	IBAN              string                  `json:"iban,omitempty"`           // EUR only
	BIC               string                  `json:"bic,omitempty"`            // EUR optional
	SourceAccount     WithdrawalSourceAccount `json:"source_account"`
	BridgeWalletID    string                  `json:"bridge_wallet_id"`
	Category          string                  `json:"category,omitempty"`
	Narration         string                  `json:"narration,omitempty"`
	Emergency         bool                    `json:"emergency"` // If true, bypass stash lock with penalty fee
	IdempotencyKey    string                  // Optional client-provided idempotency key
}

// Validate validates the fiat withdrawal request
func (r *InitiateFiatWithdrawalRequest) Validate() error {
	if r.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if r.Amount.LessThan(MinWithdrawalAmount) {
		return fmt.Errorf("amount must be at least %s", MinWithdrawalAmount.String())
	}
	if r.Amount.GreaterThan(MaxWithdrawalAmount) {
		return fmt.Errorf("amount must be at most %s per transaction", MaxWithdrawalAmount.String())
	}
	if !ValidWithdrawalCurrencies[r.Currency] {
		return fmt.Errorf("invalid currency: %s", r.Currency)
	}
	if strings.TrimSpace(r.AccountHolderName) == "" {
		return fmt.Errorf("account holder name is required")
	}
	if r.Currency == WithdrawalCurrencyEUR {
		minAmountEUR := decimal.NewFromFloat(10.00)
		if r.Amount.LessThan(minAmountEUR) {
			return fmt.Errorf("amount must be at least %s EUR", minAmountEUR.String())
		}
		iban := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(r.IBAN), " ", ""))
		if len(iban) < 15 || len(iban) > 34 {
			return fmt.Errorf("IBAN must be between 15 and 34 characters")
		}
		if !isUpperAlphaNumeric(iban) {
			return fmt.Errorf("IBAN must contain only letters and digits")
		}
		bic := strings.ToUpper(strings.TrimSpace(r.BIC))
		if bic != "" {
			if (len(bic) != 8 && len(bic) != 11) || !isUpperAlphaNumeric(bic) {
				return fmt.Errorf("BIC must be 8 or 11 alphanumeric characters")
			}
		}
	}
	if r.Currency == WithdrawalCurrencyGBP {
		sortCode := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(r.RoutingNumber), " ", ""), "-", "")
		account := strings.ReplaceAll(strings.TrimSpace(r.AccountNumber), " ", "")
		if len(sortCode) != 6 || !isDigitsOnly(sortCode) {
			return fmt.Errorf("sort code must be exactly 6 digits")
		}
		if len(account) != 8 || !isDigitsOnly(account) {
			return fmt.Errorf("account number must be exactly 8 digits for GBP")
		}
	}
	if r.Currency == WithdrawalCurrencyUSD {
		routing := strings.ReplaceAll(strings.TrimSpace(r.RoutingNumber), " ", "")
		account := strings.ReplaceAll(strings.TrimSpace(r.AccountNumber), " ", "")
		if len(routing) != 9 || !isDigitsOnly(routing) {
			return fmt.Errorf("routing number must be 9 digits")
		}
		if len(account) < 4 || len(account) > 17 || !isDigitsOnly(account) {
			return fmt.Errorf("account number must be 4-17 digits")
		}
	}
	if !ValidWithdrawalSourceAccounts[r.SourceAccount] {
		return fmt.Errorf("invalid source account: %s", r.SourceAccount)
	}
	if strings.TrimSpace(r.BridgeWalletID) == "" {
		return fmt.Errorf("bridge wallet ID is required")
	}
	return nil
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func isUpperAlphaNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'Z') {
			continue
		}
		return false
	}
	return true
}

// InitiateWithdrawalResponse represents the response to a withdrawal request
type InitiateWithdrawalResponse struct {
	WithdrawalID uuid.UUID        `json:"withdrawal_id"`
	Status       WithdrawalStatus `json:"status"`
	Message      string           `json:"message"`
	EmergencyFee decimal.Decimal  `json:"emergency_fee,omitempty"` // Fee charged for emergency stash withdrawal
}

// EmergencyWithdrawalPreviewResponse is returned by the preview endpoint.
type EmergencyWithdrawalPreviewResponse struct {
	Amount      decimal.Decimal `json:"amount"`
	FeePercent  decimal.Decimal `json:"fee_percent"`
	FeeAmount   decimal.Decimal `json:"fee_amount"`
	NetAmount   decimal.Decimal `json:"net_amount"`
	LockAgeDays int             `json:"lock_age_days"`
	FeeTier     string          `json:"fee_tier"`
}

// EmergencyWithdrawalResult is returned after executing an emergency stash-to-spending transfer.
type EmergencyWithdrawalResult struct {
	Amount     decimal.Decimal `json:"amount"`
	Fee        decimal.Decimal `json:"fee"`
	FeePercent decimal.Decimal `json:"fee_percent"`
	NetAmount  decimal.Decimal `json:"net_amount"`
	TransferID uuid.UUID       `json:"transfer_id"`
}

// WithdrawalFee represents withdrawal fee information
type WithdrawalFee struct {
	Amount    decimal.Decimal    `json:"amount"`
	Currency  WithdrawalCurrency `json:"currency"`
	Breakdown struct {
		NetworkFee decimal.Decimal `json:"network_fee"`
		ServiceFee decimal.Decimal `json:"service_fee"`
	} `json:"breakdown"`
}

// StablecoinToWithdrawalCurrency maps a Stablecoin to its WithdrawalCurrency equivalent.
func StablecoinToWithdrawalCurrency(s Stablecoin) WithdrawalCurrency {
	switch s {
	case StablecoinUSDC:
		return WithdrawalCurrencyUSDC
	case StablecoinUSDT:
		return WithdrawalCurrencyUSDT
	case StablecoinEURC:
		return WithdrawalCurrencyEURC
	case StablecoinPYUSD:
		return WithdrawalCurrencyPYUSD
	case StablecoinUSDG:
		return WithdrawalCurrencyUSDG
	default:
		return WithdrawalCurrencyUSDC
	}
}

// WithdrawalCurrencyToStablecoin maps a WithdrawalCurrency to its Stablecoin equivalent.
// Returns empty string for fiat currencies.
func WithdrawalCurrencyToStablecoin(c WithdrawalCurrency) Stablecoin {
	switch c {
	case WithdrawalCurrencyUSDC:
		return StablecoinUSDC
	case WithdrawalCurrencyUSDT:
		return StablecoinUSDT
	case WithdrawalCurrencyEURC:
		return StablecoinEURC
	case WithdrawalCurrencyPYUSD:
		return StablecoinPYUSD
	case WithdrawalCurrencyUSDG:
		return StablecoinUSDG
	default:
		return ""
	}
}

// IsStablecoinCurrency returns true if the withdrawal currency is a stablecoin (not fiat).
func (c WithdrawalCurrency) IsStablecoinCurrency() bool {
	return c == WithdrawalCurrencyUSDC || c == WithdrawalCurrencyUSDT ||
		c == WithdrawalCurrencyEURC || c == WithdrawalCurrencyPYUSD || c == WithdrawalCurrencyUSDG
}
