package di

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/umbrawallet"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/umbra"
)

// UmbraShielderAdapter adapts umbra.Client + umbrawallet.Service to allocation.UmbraShielder.
// It initializes the sidecar with the user's decrypted key before each shield operation.
type UmbraShielderAdapter struct {
	client        *umbra.Client
	walletService *umbrawallet.Service
}

// ShieldFunds initializes the sidecar for this user, then shields the tokens.
func (a *UmbraShielderAdapter) ShieldFunds(ctx context.Context, userID uuid.UUID, mint string, amount string) error {
	// Init sidecar with this user's decrypted private key
	if _, err := a.walletService.InitSidecar(ctx, userID); err != nil {
		return fmt.Errorf("init sidecar for user %s: %w", userID, err)
	}

	// Shield the funds
	if _, err := a.client.Shield(ctx, &umbra.ShieldRequest{Mint: mint, Amount: amount}); err != nil {
		return fmt.Errorf("shield funds: %w", err)
	}
	return nil
}

// UmbraProvisionerAdapter adapts umbrawallet.Service to onboarding.UmbraWalletProvisioner.
type UmbraProvisionerAdapter struct {
	walletService *umbrawallet.Service
}

func (a *UmbraProvisionerAdapter) ProvisionWallet(ctx context.Context, userID uuid.UUID) error {
	_, err := a.walletService.ProvisionWallet(ctx, userID)
	return err
}
