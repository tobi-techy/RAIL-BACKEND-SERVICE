package chainrouting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCircleChainToChainRailsChainMapsAmoyToPolygonTestnet(t *testing.T) {
	require.Equal(t, "POLYGON_TESTNET", CircleChainToChainRailsChain("MATIC-AMOY"))
	require.Equal(t, "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", USDCTokenForChainRailsChain("POLYGON_TESTNET"))
}
