package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type WaitlistRepository interface {
	Create(ctx context.Context, user *entities.WaitlistUser) error
	GetByEmail(ctx context.Context, email string) (*entities.WaitlistUser, error)
	GetByReferralCode(ctx context.Context, code string) (*entities.WaitlistUser, error)
	List(ctx context.Context, status *entities.WaitlistStatus, limit, offset int) ([]entities.WaitlistUser, int, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WaitlistStatus) error
	MarkConverted(ctx context.Context, email string, userID uuid.UUID) error
	Count(ctx context.Context) (int, error)
}
