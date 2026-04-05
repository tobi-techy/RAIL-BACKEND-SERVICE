package unit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
)

func TestCreateDepositEntries(t *testing.T) {
	userID := uuid.New()
	systemID := uuid.New()

	tests := []struct {
		name        string
		amount      decimal.Decimal
		wantLen     int
		wantDebitID uuid.UUID
		wantCreditID uuid.UUID
	}{
		{
			name:        "valid deposit creates balanced debit/credit pair",
			amount:      decimal.NewFromFloat(100.00),
			wantLen:     2,
			wantDebitID: userID,
			wantCreditID: systemID,
		},
		{
			name:    "zero amount still creates entries",
			amount:  decimal.Zero,
			wantLen: 2,
		},
		{
			name:    "negative amount still creates entries (validation is upstream)",
			amount:  decimal.NewFromFloat(-50.00),
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := ledger.CreateDepositEntries(userID, systemID, tt.amount)
			require.Len(t, entries, tt.wantLen)

			debit := entries[0]
			credit := entries[1]

			assert.Equal(t, entities.EntryTypeDebit, debit.EntryType)
			assert.Equal(t, entities.EntryTypeCredit, credit.EntryType)
			assert.Equal(t, userID, debit.AccountID)
			assert.Equal(t, systemID, credit.AccountID)
			assert.True(t, debit.Amount.Equal(tt.amount), "debit amount mismatch")
			assert.True(t, credit.Amount.Equal(tt.amount), "credit amount mismatch")
			assert.Equal(t, "USDC", debit.Currency)
			assert.Equal(t, "USDC", credit.Currency)
		})
	}
}

func TestCreateWithdrawalEntries(t *testing.T) {
	userID := uuid.New()
	systemID := uuid.New()

	tests := []struct {
		name   string
		amount decimal.Decimal
	}{
		{
			name:   "valid withdrawal reverses deposit direction",
			amount: decimal.NewFromFloat(50.00),
		},
		{
			name:   "large withdrawal",
			amount: decimal.NewFromFloat(9999.99),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := ledger.CreateWithdrawalEntries(userID, systemID, tt.amount)
			require.Len(t, entries, 2)

			// Withdrawal: user credited (decreases asset), system debited
			assert.Equal(t, entities.EntryTypeCredit, entries[0].EntryType)
			assert.Equal(t, userID, entries[0].AccountID)
			assert.Equal(t, entities.EntryTypeDebit, entries[1].EntryType)
			assert.Equal(t, systemID, entries[1].AccountID)
			assert.True(t, entries[0].Amount.Equal(entries[1].Amount), "entries must be balanced")
		})
	}
}

func TestCreateInvestmentEntries(t *testing.T) {
	pendingID := uuid.New()
	fiatID := uuid.New()
	amount := decimal.NewFromFloat(300.00)

	entries := ledger.CreateInvestmentEntries(pendingID, fiatID, amount)
	require.Len(t, entries, 2)

	assert.Equal(t, entities.EntryTypeCredit, entries[0].EntryType)
	assert.Equal(t, pendingID, entries[0].AccountID)
	assert.Equal(t, "USDC", entries[0].Currency)

	assert.Equal(t, entities.EntryTypeDebit, entries[1].EntryType)
	assert.Equal(t, fiatID, entries[1].AccountID)
	assert.Equal(t, "USD", entries[1].Currency)
}

func TestEntryBuilderValidate(t *testing.T) {
	tests := []struct {
		name    string
		build   func() *ledger.EntryBuilder
		wantErr string
	}{
		{
			name: "empty builder fails - less than 2 entries",
			build: func() *ledger.EntryBuilder {
				return ledger.NewEntryBuilder()
			},
			wantErr: "at least 2 entries",
		},
		{
			name: "single entry fails",
			build: func() *ledger.EntryBuilder {
				desc := "test"
				return ledger.NewEntryBuilder().AddDebit(uuid.New(), decimal.NewFromInt(100), "USDC", &desc)
			},
			wantErr: "at least 2 entries",
		},
		{
			name: "unbalanced entries fail",
			build: func() *ledger.EntryBuilder {
				desc := "test"
				return ledger.NewEntryBuilder().
					AddDebit(uuid.New(), decimal.NewFromInt(100), "USDC", &desc).
					AddCredit(uuid.New(), decimal.NewFromInt(50), "USDC", &desc)
			},
			wantErr: "unbalanced",
		},
		{
			name: "balanced entries pass",
			build: func() *ledger.EntryBuilder {
				desc := "test"
				return ledger.NewEntryBuilder().
					AddDebit(uuid.New(), decimal.NewFromInt(100), "USDC", &desc).
					AddCredit(uuid.New(), decimal.NewFromInt(100), "USDC", &desc)
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build().Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
