package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// KYCUserRepositoryAdapter adapts UserRepository to the KYC service interface.
type KYCUserRepositoryAdapter struct {
	userRepo *UserRepository
}

func NewKYCUserRepositoryAdapter(userRepo *UserRepository) *KYCUserRepositoryAdapter {
	return &KYCUserRepositoryAdapter{userRepo: userRepo}
}

func (a *KYCUserRepositoryAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	return a.userRepo.GetUserEntityByID(ctx, id)
}

func (a *KYCUserRepositoryAdapter) GetProfileByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error) {
	return a.userRepo.GetByID(ctx, userID)
}

func (a *KYCUserRepositoryAdapter) Update(ctx context.Context, user *entities.User) error {
	return a.userRepo.UpdateUserEntity(ctx, user)
}
