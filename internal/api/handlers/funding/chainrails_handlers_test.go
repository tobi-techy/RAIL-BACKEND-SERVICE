package funding

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
)

func TestMapChainRailsChain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want entities.Chain
	}{
		{name: "solana mainnet", in: "SOLANA_MAINNET", want: entities.ChainSOL},
		{name: "solana testnet", in: "SOLANA_TESTNET", want: entities.ChainSOL},
		{name: "base mainnet", in: "BASE_MAINNET", want: entities.ChainBase},
		{name: "ethereum mainnet", in: "ETHEREUM_MAINNET", want: entities.ChainETH},
		{name: "unknown fallback", in: "UNKNOWN_CHAIN", want: entities.ChainBase},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, mapChainRailsChain(tt.in))
		})
	}
}
