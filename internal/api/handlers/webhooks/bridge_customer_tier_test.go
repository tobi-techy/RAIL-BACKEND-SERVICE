package webhooks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type fakeBridgeCustomerRepo struct {
	user        *entities.UserProfile
	tierUpdated int
	bridgeKYC   string
	kycStatus   entities.KYCStatus
}

func (r *fakeBridgeCustomerRepo) GetByBridgeCustomerID(ctx context.Context, bridgeCustomerID string) (*entities.UserProfile, error) {
	return r.user, nil
}
func (r *fakeBridgeCustomerRepo) UpdateBridgeKYCStatus(ctx context.Context, userID uuid.UUID, status string) error {
	r.bridgeKYC = status
	return nil
}
func (r *fakeBridgeCustomerRepo) UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status entities.KYCStatus, approvedAt *time.Time, rejectionReason *string) error {
	r.kycStatus = status
	return nil
}
func (r *fakeBridgeCustomerRepo) UpdateOnboardingStatus(ctx context.Context, userID uuid.UUID, status entities.OnboardingStatus) error {
	return nil
}
func (r *fakeBridgeCustomerRepo) UpdateKYCTier(ctx context.Context, userID uuid.UUID, tier int) error {
	r.tierUpdated = tier
	return nil
}

func TestBridgeCustomerActive_PromotesTier3(t *testing.T) {
	repo := &fakeBridgeCustomerRepo{
		user: &entities.UserProfile{ID: uuid.New(), KYCTier: entities.KYCTierLevelBasic},
	}
	proc := NewBridgeCustomerStatusProcessor(repo, zap.NewNop())

	if err := proc.UpdateCustomerStatus(context.Background(), "cust_1", "active"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.tierUpdated != entities.KYCTierLevelAdvanced {
		t.Errorf("expected tier promoted to advanced (3), got %d", repo.tierUpdated)
	}
	if repo.kycStatus != entities.KYCStatusApproved {
		t.Errorf("expected kyc_status approved, got %v", repo.kycStatus)
	}
}

func TestBridgeCustomerNotActive_NoTierPromotion(t *testing.T) {
	repo := &fakeBridgeCustomerRepo{
		user: &entities.UserProfile{ID: uuid.New(), KYCTier: entities.KYCTierLevelBasic},
	}
	proc := NewBridgeCustomerStatusProcessor(repo, zap.NewNop())

	if err := proc.UpdateCustomerStatus(context.Background(), "cust_1", "under_review"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.tierUpdated != 0 {
		t.Errorf("tier must not be promoted when Bridge is not active, got %d", repo.tierUpdated)
	}
}
