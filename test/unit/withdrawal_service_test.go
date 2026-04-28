package unit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
)

// TestCalculateFiatWithdrawalFee tests the fiat flat fee calculation.
func TestCalculateFiatWithdrawalFee(t *testing.T) {
	svc := withdrawal.NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name     string
		amount   string
		currency entities.WithdrawalCurrency
		wantFee  string
	}{
		{
			name:     "USD 100 → flat $1.00",
			amount:   "100",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "1",
		},
		{
			name:     "USD 10 → flat $1.00",
			amount:   "10",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "1",
		},
		{
			name:     "EUR 100 → flat €1.00",
			amount:   "100",
			currency: entities.WithdrawalCurrencyEUR,
			wantFee:  "1",
		},
		{
			name:     "USD 1000 → flat $1.00",
			amount:   "1000",
			currency: entities.WithdrawalCurrencyUSD,
			wantFee:  "1",
		},
		{
			name:     "USDC → default USD flat $1.00",
			amount:   "100",
			currency: entities.WithdrawalCurrencyUSDC,
			wantFee:  "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := decimal.RequireFromString(tt.amount)
			feeResp, err := svc.GetWithdrawalFee(context.Background(), entities.WithdrawalTypeFiat, amount, tt.currency, "", "")
			require.NoError(t, err)
			expected := decimal.RequireFromString(tt.wantFee)
			assert.True(t, feeResp.Amount.Equal(expected),
				"fee: got %s, want %s", feeResp.Amount, tt.wantFee)
		})
	}
}

// TestCalculateCryptoWithdrawalFee verifies crypto withdrawals use destination-chain flat fees.
func TestCalculateCryptoWithdrawalFee(t *testing.T) {
	svc := withdrawal.NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name    string
		amount  string
		src     string
		dst     string
		wantFee string
	}{
		{"same chain SOL→SOL", "100", "SOL", "SOL", "0.1"},
		{"cross chain SOL→ETH", "500", "SOL", "ETH", "0.5"},
		{"BASE→MATIC", "50", "BASE", "MATIC", "0.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := decimal.RequireFromString(tt.amount)
			feeResp, err := svc.GetWithdrawalFee(context.Background(), entities.WithdrawalTypeCrypto, amount, entities.WithdrawalCurrencyUSDC, tt.src, tt.dst)
			require.NoError(t, err)
			expected := decimal.RequireFromString(tt.wantFee)
			assert.True(t, feeResp.Amount.Equal(expected),
				"crypto fee: got %s, want %s", feeResp.Amount, tt.wantFee)
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
