package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type PlatformIdentityRepository interface {
	GetByPlatformUser(ctx context.Context, platform entities.Platform, platformUserID string) (*entities.PlatformIdentity, error)
	GetByID(ctx context.Context, id uuid.UUID) (*entities.PlatformIdentity, error)
	GetByUserAndPlatform(ctx context.Context, userID uuid.UUID, platform entities.Platform) (*entities.PlatformIdentity, error)
	GetByHandshakeTokenHash(ctx context.Context, hash string) (*entities.PlatformIdentity, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.PlatformIdentity, error)
	Create(ctx context.Context, pi *entities.PlatformIdentity) error
	SetHandshake(ctx context.Context, id uuid.UUID, tokenHash string, expiresAt time.Time) error
	CompleteHandshake(ctx context.Context, id uuid.UUID, platformUserID string) error
	TouchLastUsed(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type UserResolver struct {
	platformRepo PlatformIdentityRepository
}

func NewUserResolver(platformRepo PlatformIdentityRepository) *UserResolver {
	return &UserResolver{platformRepo: platformRepo}
}

type ResolvedUser struct {
	UserID   uuid.UUID
	Identity *entities.PlatformIdentity
}

func (r *UserResolver) Resolve(ctx context.Context, platform entities.Platform, platformUserID string) (*ResolvedUser, error) {
	identity, err := r.platformRepo.GetByPlatformUser(ctx, platform, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("unlinked platform user: %w", err)
	}
	if identity.LinkedAt == nil {
		return nil, fmt.Errorf("platform user not yet linked (handshake pending)")
	}
	if err := r.platformRepo.TouchLastUsed(ctx, identity.ID); err != nil {
		return nil, fmt.Errorf("touch last used: %w", err)
	}
	return &ResolvedUser{UserID: identity.UserID, Identity: identity}, nil
}
