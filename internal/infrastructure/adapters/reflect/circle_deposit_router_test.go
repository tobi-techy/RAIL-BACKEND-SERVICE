package reflect

import (
	"testing"

	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestChainRailsFundingAmount(t *testing.T) {
	intent := &chainrailspkg.CreateIntentResponse{
		ID:                      42,
		TotalAmountInAssetToken: "1001250",
		AssetTokenDecimals:      6,
	}

	amount, err := chainRailsFundingAmount(intent, decimal.RequireFromString("1.000000"))

	require.NoError(t, err)
	require.True(t, amount.Equal(decimal.RequireFromString("1.001250")))
}

func TestChainRailsFundingAmountRejectsUnderfundedIntent(t *testing.T) {
	intent := &chainrailspkg.CreateIntentResponse{
		ID:                      42,
		TotalAmountInAssetToken: "999999",
		AssetTokenDecimals:      6,
	}

	_, err := chainRailsFundingAmount(intent, decimal.RequireFromString("1.000000"))

	require.Error(t, err)
}

func TestNormalizeReflectChainRailsDestinationUsesTestnetForTestnetSources(t *testing.T) {
	require.Equal(t,
		"SOLANA_TESTNET",
		normalizeReflectChainRailsDestination("BASE_TESTNET", "SOLANA_MAINNET"),
	)
	require.Equal(t,
		"SOLANA_MAINNET",
		normalizeReflectChainRailsDestination("BASE_MAINNET", "SOLANA_MAINNET"),
	)
}
