package platform

import (
	"context"
	"database/sql"
	"errors"
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
	ListLinkedByPlatform(ctx context.Context, platform entities.Platform) ([]*entities.PlatformIdentity, error)
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
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("unlinked platform user")
	}
	if err != nil {
		// A real lookup failure (DB/Redis blip) is NOT "unlinked". Treating it
		// as unlinked would demote a linked user into the guest/onboarding path
		// mid-conversation and answer from a different brain. Mark it transient
		// so the caller requeues instead of guessing.
		return nil, Retryable(fmt.Errorf("lookup platform identity: %w", err))
	}
	if identity.LinkedAt == nil {
		return nil, fmt.Errorf("platform user not yet linked (handshake pending)")
	}
	// TouchLastUsed is bookkeeping only. A write blip here must never un-link a
	// verified user mid-conversation, so the identity still resolves.
	if err := r.platformRepo.TouchLastUsed(ctx, identity.ID); err != nil {
		return &ResolvedUser{UserID: identity.UserID, Identity: identity}, nil
	}
	return &ResolvedUser{UserID: identity.UserID, Identity: identity}, nil
}
