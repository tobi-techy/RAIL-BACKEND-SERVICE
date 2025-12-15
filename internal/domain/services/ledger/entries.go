package ledger

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// EntryBuilder helps construct common ledger transaction patterns
type EntryBuilder struct {
	entries []entities.CreateEntryRequest
}

// NewEntryBuilder creates a new entry builder
func NewEntryBuilder() *EntryBuilder {
	return &EntryBuilder{
		entries: make([]entities.CreateEntryRequest, 0, 2),
	}
}

// AddDebit adds a debit entry
func (b *EntryBuilder) AddDebit(accountID uuid.UUID, amount decimal.Decimal, currency string, description *string) *EntryBuilder {
	b.entries = append(b.entries, entities.CreateEntryRequest{
		AccountID:   accountID,
		EntryType:   entities.EntryTypeDebit,
		Amount:      amount,
		Currency:    currency,
		Description: description,
	})
	return b
}

// AddCredit adds a credit entry
func (b *EntryBuilder) AddCredit(accountID uuid.UUID, amount decimal.Decimal, currency string, description *string) *EntryBuilder {
	b.entries = append(b.entries, entities.CreateEntryRequest{
		AccountID:   accountID,
		EntryType:   entities.EntryTypeCredit,
		Amount:      amount,
		Currency:    currency,
		Description: description,
	})
	return b
}

// Build returns the constructed entries
func (b *EntryBuilder) Build() []entities.CreateEntryRequest {
	return b.entries
}

// Validate ensures the entries are balanced
func (b *EntryBuilder) Validate() error {
	if len(b.entries) < 2 {
		return fmt.Errorf("transaction must have at least 2 entries")
	}

	var debitSum, creditSum decimal.Decimal
	for _, entry := range b.entries {
		if entry.EntryType == entities.EntryTypeDebit {
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

// CreateDepositEntries creates entries for a USDC deposit
// User receives USDC, system buffer decreases
func CreateDepositEntries(userAccountID, systemBufferID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "USDC deposit"
	return NewEntryBuilder().
		AddDebit(userAccountID, amount, "USDC", &desc).
		AddCredit(systemBufferID, amount, "USDC", &desc).
		Build()
}

// CreateWithdrawalEntries creates entries for a USDC withdrawal
// User loses USDC, system buffer increases
func CreateWithdrawalEntries(userAccountID, systemBufferID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "USDC withdrawal"
	return NewEntryBuilder().
		AddCredit(userAccountID, amount, "USDC", &desc).
		AddDebit(systemBufferID, amount, "USDC", &desc).
		Build()
}

// CreateConversionUSDCToUSDEntries creates entries for USDC → USD conversion
// System USDC buffer decreases, system fiat buffer increases
func CreateConversionUSDCToUSDEntries(usdcBufferID, fiatBufferID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "USDC to USD conversion"
	return NewEntryBuilder().
		AddCredit(usdcBufferID, amount, "USDC", &desc).
		AddDebit(fiatBufferID, amount, "USD", &desc).
		Build()
}

// CreateConversionUSDToUSDCEntries creates entries for USD → USDC conversion
// System fiat buffer decreases, system USDC buffer increases
func CreateConversionUSDToUSDCEntries(fiatBufferID, usdcBufferID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "USD to USDC conversion"
	return NewEntryBuilder().
		AddCredit(fiatBufferID, amount, "USD", &desc).
		AddDebit(usdcBufferID, amount, "USDC", &desc).
		Build()
}

// CreateBrokerFundingEntries creates entries for funding Alpaca broker account
// System fiat buffer decreases, broker operational increases
func CreateBrokerFundingEntries(fiatBufferID, brokerOperationalID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "Broker account funding"
	return NewEntryBuilder().
		AddCredit(fiatBufferID, amount, "USD", &desc).
		AddDebit(brokerOperationalID, amount, "USD", &desc).
		Build()
}

// CreateInvestmentEntries creates entries for an investment execution
// User's pending investment decreases, fiat exposure increases
func CreateInvestmentEntries(pendingInvestmentID, fiatExposureID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "Investment execution"
	return NewEntryBuilder().
		AddCredit(pendingInvestmentID, amount, "USDC", &desc).
		AddDebit(fiatExposureID, amount, "USD", &desc).
		Build()
}

// CreateDeinvestmentEntries creates entries for selling investments
// User's fiat exposure decreases, USDC balance increases
func CreateDeinvestmentEntries(fiatExposureID, usdcBalanceID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "Investment liquidation"
	return NewEntryBuilder().
		AddCredit(fiatExposureID, amount, "USD", &desc).
		AddDebit(usdcBalanceID, amount, "USDC", &desc).
		Build()
}

// CreateReservationEntries creates entries for reserving funds for investment
// User's USDC balance decreases, pending investment increases
func CreateReservationEntries(usdcBalanceID, pendingInvestmentID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "Reserve funds for investment"
	return NewEntryBuilder().
		AddCredit(usdcBalanceID, amount, "USDC", &desc).
		AddDebit(pendingInvestmentID, amount, "USDC", &desc).
		Build()
}

// CreateReleaseReservationEntries creates entries for releasing reserved funds
// User's pending investment decreases, USDC balance increases
func CreateReleaseReservationEntries(pendingInvestmentID, usdcBalanceID uuid.UUID, amount decimal.Decimal) []entities.CreateEntryRequest {
	desc := "Release reserved funds"
	return NewEntryBuilder().
		AddCredit(pendingInvestmentID, amount, "USDC", &desc).
		AddDebit(usdcBalanceID, amount, "USDC", &desc).
		Build()
}

// CreateBufferReplenishmentEntries creates entries for replenishing system buffers
// Generic buffer transfer
func CreateBufferReplenishmentEntries(sourceBufferID, destBufferID uuid.UUID, amount decimal.Decimal, currency string) []entities.CreateEntryRequest {
	desc := "Buffer replenishment"
	return NewEntryBuilder().
		AddCredit(sourceBufferID, amount, currency, &desc).
		AddDebit(destBufferID, amount, currency, &desc).
		Build()
}

// TransactionRequestBuilder helps construct complete transaction requests
type TransactionRequestBuilder struct {
	req *entities.CreateTransactionRequest
}

// NewTransactionRequestBuilder creates a new transaction request builder
func NewTransactionRequestBuilder() *TransactionRequestBuilder {
	return &TransactionRequestBuilder{
		req: &entities.CreateTransactionRequest{
			Entries: make([]entities.CreateEntryRequest, 0),
		},
	}
}

// WithUser sets the user ID
func (b *TransactionRequestBuilder) WithUser(userID uuid.UUID) *TransactionRequestBuilder {
	b.req.UserID = &userID
	return b
}

// WithType sets the transaction type
func (b *TransactionRequestBuilder) WithType(txType entities.TransactionType) *TransactionRequestBuilder {
	b.req.TransactionType = txType
	return b
}

// WithReference sets the reference ID and type
func (b *TransactionRequestBuilder) WithReference(referenceID uuid.UUID, referenceType string) *TransactionRequestBuilder {
	b.req.ReferenceID = &referenceID
	b.req.ReferenceType = &referenceType
	return b
}

// WithIdempotencyKey sets the idempotency key
func (b *TransactionRequestBuilder) WithIdempotencyKey(key string) *TransactionRequestBuilder {
	b.req.IdempotencyKey = key
	return b
}

// WithDescription sets the description
func (b *TransactionRequestBuilder) WithDescription(description string) *TransactionRequestBuilder {
	b.req.Description = &description
	return b
}

// WithMetadata sets the metadata
func (b *TransactionRequestBuilder) WithMetadata(metadata map[string]any) *TransactionRequestBuilder {
	b.req.Metadata = metadata
	return b
}

// WithEntries sets the entries
func (b *TransactionRequestBuilder) WithEntries(entries []entities.CreateEntryRequest) *TransactionRequestBuilder {
	b.req.Entries = entries
	return b
}

// AddEntry adds a single entry
func (b *TransactionRequestBuilder) AddEntry(entry entities.CreateEntryRequest) *TransactionRequestBuilder {
	b.req.Entries = append(b.req.Entries, entry)
	return b
}

// Build returns the constructed request
func (b *TransactionRequestBuilder) Build() (*entities.CreateTransactionRequest, error) {
	if err := b.req.Validate(); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}
	return b.req, nil
}

// GenerateIdempotencyKey generates a unique idempotency key for a transaction
func GenerateIdempotencyKey(prefix string, userID uuid.UUID, referenceID uuid.UUID) string {
	return fmt.Sprintf("%s-%s-%s", prefix, userID.String(), referenceID.String())
}

// GenerateIdempotencyKeyWithAmount generates a unique idempotency key with amount
func GenerateIdempotencyKeyWithAmount(prefix string, userID uuid.UUID, amount decimal.Decimal, timestamp int64) string {
	return fmt.Sprintf("%s-%s-%s-%d", prefix, userID.String(), amount.String(), timestamp)
}
