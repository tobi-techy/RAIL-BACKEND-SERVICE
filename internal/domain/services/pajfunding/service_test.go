package pajfunding

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCalculateOfframpTransferAmountsDoesNotSendRailFeeToPaj(t *testing.T) {
	totalHold := decimal.RequireFromString("0.36")
	railFee := decimal.RequireFromString("0.02")

	transferAmount, refund, err := calculateOfframpTransferAmounts(0.34, totalHold, railFee)

	require.NoError(t, err)
	require.True(t, decimal.RequireFromString("0.34").Equal(transferAmount))
	require.True(t, decimal.Zero.Equal(refund))
}

func TestCalculateOfframpTransferAmountsRefundsSlippageButKeepsRailFee(t *testing.T) {
	totalHold := decimal.RequireFromString("0.37")
	railFee := decimal.RequireFromString("0.02")

	transferAmount, refund, err := calculateOfframpTransferAmounts(0.34, totalHold, railFee)

	require.NoError(t, err)
	require.True(t, decimal.RequireFromString("0.34").Equal(transferAmount))
	require.True(t, decimal.RequireFromString("0.01").Equal(refund))
}

func TestCalculateOfframpTransferAmountsRejectsTransferAboveHold(t *testing.T) {
	totalHold := decimal.RequireFromString("0.35")
	railFee := decimal.RequireFromString("0.02")

	_, _, err := calculateOfframpTransferAmounts(0.34, totalHold, railFee)

	require.Error(t, err)
}
