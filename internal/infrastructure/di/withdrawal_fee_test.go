package di

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestWithdrawalPlatformFeeFromMetadata(t *testing.T) {
	total := decimal.RequireFromString("100.50")

	tests := []struct {
		name     string
		metadata map[string]interface{}
		want     decimal.Decimal
		wantErr  bool
	}{
		{
			name:     "withdrawal fee amount",
			metadata: map[string]interface{}{"fee_amount": "0.50"},
			want:     decimal.RequireFromString("0.50"),
		},
		{
			name:     "paj rail fee",
			metadata: map[string]interface{}{"rail_fee": "0.02"},
			want:     decimal.RequireFromString("0.02"),
		},
		{
			name:     "no fee metadata",
			metadata: map[string]interface{}{"provider": "chainrails"},
			want:     decimal.Zero,
		},
		{
			name:     "fee cannot exceed total",
			metadata: map[string]interface{}{"fee_amount": "101"},
			wantErr:  true,
		},
		{
			name:     "negative fee rejected",
			metadata: map[string]interface{}{"fee_amount": "-0.01"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withdrawalPlatformFeeFromMetadata(tt.metadata, total)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, tt.want.Equal(got), "got %s want %s", got, tt.want)
		})
	}
}
