package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// UserProfileAdapter adapts UserRepository to UserProfileRepository interface
type UserProfileAdapter struct {
	userRepo *UserRepository
}

func NewUserProfileAdapter(userRepo *UserRepository) *UserProfileAdapter {
	return &UserProfileAdapter{userRepo: userRepo}
}

// GetByUserID returns user profile by user ID
func (a *UserProfileAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error) {
	return a.userRepo.GetByID(ctx, userID)
}
