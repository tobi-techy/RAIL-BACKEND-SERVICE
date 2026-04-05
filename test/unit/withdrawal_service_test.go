package unit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
)

// TestCalculateFiatWithdrawalFee tests the fiat fee calculation:
// USD: 1% + $0.50, EUR: 1% + €0.50
func TestCalculateFiatWithdrawalFee(t *testing.T) {
	svc := withdrawal.NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name     string
		amount   string
		currency entities.WithdrawalCurrency
		wantFee  string
	}{
		{
			name:     "USD 100 → 1% + $0.50 = $1.50",
			amount:   "100",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "1.5",
		},
		{
			name:     "USD 10 minimum → $0.60",
			amount:   "10",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "0.6",
		},
		{
			name:     "EUR 100 → 1% + €0.50 = €1.50",
			amount:   "100",
			currency: entities.WithdrawalCurrencyEUR,
			wantFee:  "1.5",
		},
		{
			name:     "USD 1000 → $10.50",
			amount:   "1000",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "10.5",
		},
		{
			name:     "unknown currency → zero fee",
			amount:   "100",
			currency: entities.WithdrawalCurrencyUSDC,
			wantFee:  "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := decimal.RequireFromString(tt.amount)
			// GetWithdrawalFee is the public method that wraps calculateFiatWithdrawalFee
			feeResp, err := svc.GetWithdrawalFee(nil, entities.WithdrawalTypeFiat, amount, tt.currency, "", "")
			require.NoError(t, err)
			expected := decimal.RequireFromString(tt.wantFee)
			assert.True(t, feeResp.Amount.Equal(expected),
				"fee: got %s, want %s", feeResp.Amount, tt.wantFee)
		})
	}
}

// TestCalculateCryptoWithdrawalFee verifies crypto withdrawals are fee-free
func TestCalculateCryptoWithdrawalFee(t *testing.T) {
	svc := withdrawal.NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name   string
		amount string
		src    string
		dst    string
	}{
		{"same chain SOL→SOL", "100", "SOL", "SOL"},
		{"cross chain SOL→ETH", "500", "SOL", "ETH"},
		{"BASE→MATIC", "50", "BASE", "MATIC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := decimal.RequireFromString(tt.amount)
			feeResp, err := svc.GetWithdrawalFee(nil, entities.WithdrawalTypeCrypto, amount, entities.WithdrawalCurrencyUSDC, tt.src, tt.dst)
			require.NoError(t, err)
			assert.True(t, feeResp.Amount.IsZero(), "crypto fee should be zero, got %s", feeResp.Amount)
		})
	}
}

// TestValidateChainPair tests the chain validation logic
func TestValidateChainPair(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		dst     string
		wantErr string
	}{
		{"valid SOL→ETH", "SOL", "ETH", ""},
		{"valid BASE→MATIC", "BASE", "MATIC", ""},
		{"valid case insensitive", "sol", "eth", ""}, // validateChainPair uppercases
		{"empty source", "", "ETH", "source chain is required"},
		{"empty destination", "SOL", "", "destination chain is required"},
		{"unsupported source", "BTC", "ETH", "unsupported source chain"},
		{"unsupported destination", "SOL", "BTC", "unsupported destination chain"},
		{"both unsupported", "BTC", "DOGE", "unsupported source chain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// validateChainPair is package-level, test via supported chains map + logic
			err := validateChainPairHelper(tt.src, tt.dst)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestGetWithdrawalFee_InvalidAmount verifies zero/negative amounts are rejected
func TestGetWithdrawalFee_InvalidAmount(t *testing.T) {
	svc := withdrawal.NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.GetWithdrawalFee(nil, entities.WithdrawalTypeFiat, decimal.Zero, entities.WithdrawalCurrencyUSD, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount must be positive")

	_, err = svc.GetWithdrawalFee(nil, entities.WithdrawalTypeFiat, decimal.NewFromFloat(-10), entities.WithdrawalCurrencyUSD, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount must be positive")
}

// validateChainPairHelper mirrors the unexported validateChainPair logic for testing
func validateChainPairHelper(sourceChain, destChain string) error {
	if sourceChain == "" {
		return fmt.Errorf("source chain is required")
	}
	if destChain == "" {
		return fmt.Errorf("destination chain is required")
	}
	src := strings.ToUpper(sourceChain)
	dst := strings.ToUpper(destChain)
	if !withdrawal.SupportedChains[src] {
		return fmt.Errorf("unsupported source chain: %s", sourceChain)
	}
	if !withdrawal.SupportedChains[dst] {
		return fmt.Errorf("unsupported destination chain: %s", destChain)
	}
	return nil
}
