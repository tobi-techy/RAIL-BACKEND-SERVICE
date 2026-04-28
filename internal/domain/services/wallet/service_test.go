package wallet

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestMapBridgeStatusToWalletStatus(t *testing.T) {
	tests := []struct {
		name         string
		bridgeStatus string
		want         entities.WalletStatus
	}{
		{
			name:         "active maps to live",
			bridgeStatus: "active",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "live maps to live",
			bridgeStatus: "live",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "ready maps to live",
			bridgeStatus: "ready",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "failed maps to failed",
			bridgeStatus: "failed",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "error maps to failed",
			bridgeStatus: "error",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "rejected maps to failed",
			bridgeStatus: "rejected",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "creating maps to creating",
			bridgeStatus: "creating",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "pending maps to creating",
			bridgeStatus: "pending",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "processing maps to creating",
			bridgeStatus: "processing",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "initializing maps to creating",
			bridgeStatus: "initializing",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "unknown status maps to creating",
			bridgeStatus: "unknown",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "empty string maps to creating",
			bridgeStatus: "",
			want:         entities.WalletStatusCreating,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapBridgeStatusToWalletStatus(tt.bridgeStatus)
			if got != tt.want {
				t.Errorf("mapBridgeStatusToWalletStatus(%q) = %v, want %v", tt.bridgeStatus, got, tt.want)
			}
		})
	}
}
