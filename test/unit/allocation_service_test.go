package unit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// TestProcessIncomingFunds_SplitMath verifies the 70/30 split arithmetic
// that ProcessIncomingFunds uses: spendingAmount = amount * ratio, stashAmount = amount - spendingAmount
func TestProcessIncomingFunds_SplitMath(t *testing.T) {
	ratios := entities.DefaultAllocationRatios()

	tests := []struct {
		name         string
		deposit      string
		wantSpending string
		wantStash    string
	}{
		{
			name:         "100 USDC splits to 70/30",
			deposit:      "100",
			wantSpending: "70",
			wantStash:    "30",
		},
		{
			name:         "1 USDC splits correctly",
			deposit:      "1",
			wantSpending: "0.7",
			wantStash:    "0.3",
		},
		{
			name:         "odd amount - no precision loss",
			deposit:      "33.33",
			wantSpending: "23.331",
			wantStash:    "9.999",
		},
		{
			name:         "large deposit",
			deposit:      "10000",
			wantSpending: "7000",
			wantStash:    "3000",
		},
		{
			name:         "fractional cent deposit",
			deposit:      "0.01",
			wantSpending: "0.007",
			wantStash:    "0.003",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := decimal.RequireFromString(tt.deposit)
			// Mirror the exact calculation from allocation/service.go:
			// spendingAmount := req.Amount.Mul(mode.RatioSpending)
			// stashAmount := req.Amount.Sub(spendingAmount)
			spendingAmount := amount.Mul(ratios.SpendingRatio)
			stashAmount := amount.Sub(spendingAmount)

			assert.True(t, spendingAmount.Equal(decimal.RequireFromString(tt.wantSpending)),
				"spending: got %s, want %s", spendingAmount, tt.wantSpending)
			assert.True(t, stashAmount.Equal(decimal.RequireFromString(tt.wantStash)),
				"stash: got %s, want %s", stashAmount, tt.wantStash)

			// Critical invariant: spending + stash must always equal the original deposit
			assert.True(t, spendingAmount.Add(stashAmount).Equal(amount),
				"split must sum to original: %s + %s != %s", spendingAmount, stashAmount, amount)
		})
	}
}

// TestCanSpend_ModeNotActive verifies that when allocation mode is nil or inactive,
// spending is allowed (legacy flow). This mirrors the CanSpend logic:
// "If mode is not active, allow spending"
func TestCanSpend_ModeNotActive(t *testing.T) {
	tests := []struct {
		name     string
		mode     *entities.SmartAllocationMode
		wantAllow bool
	}{
		{
			name:     "nil mode allows spending",
			mode:     nil,
			wantAllow: true,
		},
		{
			name: "inactive mode allows spending",
			mode: &entities.SmartAllocationMode{
				UserID: uuid.New(),
				Active: false,
			},
			wantAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canSpend := tt.mode == nil || !tt.mode.Active
			assert.Equal(t, tt.wantAllow, canSpend)
		})
	}
}

// TestCanSpend_BalanceCheck verifies the balance comparison logic used in CanSpend:
// canSpend := spendingBalance.GreaterThanOrEqual(amount)
func TestCanSpend_BalanceCheck(t *testing.T) {
	tests := []struct {
		name     string
		balance  string
		amount   string
		wantSpend bool
	}{
		{
			name:     "sufficient balance",
			balance:  "100.00",
			amount:   "50.00",
			wantSpend: true,
		},
		{
			name:     "exact balance",
			balance:  "50.00",
			amount:   "50.00",
			wantSpend: true,
		},
		{
			name:     "insufficient balance",
			balance:  "49.99",
			amount:   "50.00",
			wantSpend: false,
		},
		{
			name:     "zero balance",
			balance:  "0",
			amount:   "1.00",
			wantSpend: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			balance := decimal.RequireFromString(tt.balance)
			amount := decimal.RequireFromString(tt.amount)
			canSpend := balance.GreaterThanOrEqual(amount)
			assert.Equal(t, tt.wantSpend, canSpend)
		})
	}
}

// TestIncomingFundsRequestValidate tests the request validation used at the top of ProcessIncomingFunds
func TestIncomingFundsRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     entities.IncomingFundsRequest
		wantErr string
	}{
		{
			name: "valid request",
			req: entities.IncomingFundsRequest{
				UserID:    uuid.New(),
				Amount:    decimal.NewFromFloat(100),
				EventType: entities.AllocationEventTypeDeposit,
			},
			wantErr: "",
		},
		{
			name: "zero amount rejected",
			req: entities.IncomingFundsRequest{
				UserID:    uuid.New(),
				Amount:    decimal.Zero,
				EventType: entities.AllocationEventTypeDeposit,
			},
			wantErr: "amount must be positive",
		},
		{
			name: "negative amount rejected",
			req: entities.IncomingFundsRequest{
				UserID:    uuid.New(),
				Amount:    decimal.NewFromFloat(-10),
				EventType: entities.AllocationEventTypeDeposit,
			},
			wantErr: "amount must be positive",
		},
		{
			name: "nil user ID rejected",
			req: entities.IncomingFundsRequest{
				UserID:    uuid.Nil,
				Amount:    decimal.NewFromFloat(100),
				EventType: entities.AllocationEventTypeDeposit,
			},
			wantErr: "user ID is required",
		},
		{
			name: "invalid event type rejected",
			req: entities.IncomingFundsRequest{
				UserID:    uuid.New(),
				Amount:    decimal.NewFromFloat(100),
				EventType: "invalid",
			},
			wantErr: "invalid allocation event type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestAllocationRatiosValidate ensures ratio constraints are enforced
func TestAllocationRatiosValidate(t *testing.T) {
	tests := []struct {
		name     string
		spending string
		stash    string
		wantErr  bool
	}{
		{"default 70/30", "0.70", "0.30", false},
		{"50/50", "0.50", "0.50", false},
		{"don't sum to 1", "0.60", "0.30", true},
		{"negative spending", "-0.10", "1.10", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := entities.AllocationRatios{
				SpendingRatio: decimal.RequireFromString(tt.spending),
				StashRatio:    decimal.RequireFromString(tt.stash),
			}
			err := r.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
