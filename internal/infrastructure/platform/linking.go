package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

const handshakeTokenBytes = 32

// pendingUserIDPrefix marks a platform identity whose real platform user id is
// not yet known. The row exists only to hold the pending handshake token; the
// real sender id is written at confirmation time. Using a per-id placeholder
// keeps the (platform, platform_user_id) uniqueness constraint satisfied without
// trusting any client-supplied identifier.
const pendingUserIDPrefix = "pending:"

type LinkingService struct {
	platformRepo PlatformIdentityRepository
	now          func() time.Time
	tokenTTL     time.Duration
}

func NewLinkingService(platformRepo PlatformIdentityRepository, tokenTTLSeconds int) *LinkingService {
	ttl := time.Duration(tokenTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &LinkingService{
		platformRepo: platformRepo,
		now:          time.Now,
		tokenTTL:     ttl,
	}
}

// TokenTTLSeconds exposes the configured handshake TTL for API responses.
func (s *LinkingService) TokenTTLSeconds() int {
	return int(s.tokenTTL.Seconds())
}

type HandshakeResult struct {
	Token     string
	ExpiresAt time.Time
	Identity  *entities.PlatformIdentity
}

// InitiateHandshake issues a one-time link token for an authenticated Rail user.
// The platform identifier is deliberately NOT taken from the caller: the real
// platform user id is captured from the inbound message when the user texts the
// token back (see ConfirmHandshake). This makes possession of the token plus
// control of the platform account the proof of ownership.
func (s *LinkingService) InitiateHandshake(ctx context.Context, userID uuid.UUID, platform entities.Platform) (*HandshakeResult, error) {
	identity, err := s.platformRepo.GetByUserAndPlatform(ctx, userID, platform)
	if err != nil {
		identity = &entities.PlatformIdentity{
			ID:       uuid.New(),
			UserID:   userID,
			Platform: platform,
		}
		identity.PlatformUserID = pendingUserIDPrefix + identity.ID.String()
		if err := s.platformRepo.Create(ctx, identity); err != nil {
			return nil, fmt.Errorf("create platform identity: %w", err)
		}
	} else if identity.LinkedAt != nil {
		return nil, fmt.Errorf("this platform is already linked; unlink it first to re-link")
	}

	tokenBytes := make([]byte, handshakeTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	hash := sha256Hex(token)
	expiresAt := s.now().Add(s.tokenTTL)

	if err := s.platformRepo.SetHandshake(ctx, identity.ID, hash, expiresAt); err != nil {
		return nil, fmt.Errorf("set handshake: %w", err)
	}

	identity.HandshakeTokenHash = &hash
	identity.HandshakeExpiresAt = &expiresAt

	return &HandshakeResult{Token: token, ExpiresAt: expiresAt, Identity: identity}, nil
}

// ConfirmHandshake completes a link. It binds the identity to senderUserID — the
// platform user id observed on the inbound message that carried the token — and
// rejects the attempt if that platform account is already linked elsewhere.
func (s *LinkingService) ConfirmHandshake(ctx context.Context, token string, platform entities.Platform, senderUserID string) (*entities.PlatformIdentity, error) {
	if senderUserID == "" || strings.HasPrefix(senderUserID, pendingUserIDPrefix) {
		return nil, fmt.Errorf("missing sender identity")
	}

	hash := sha256Hex(token)
	identity, err := s.platformRepo.GetByHandshakeTokenHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired handshake token")
	}
	if identity.Platform != platform {
		return nil, fmt.Errorf("link code was issued for a different platform")
	}
	if identity.LinkedAt != nil {
		return nil, fmt.Errorf("link code has already been used")
	}
	if identity.HandshakeExpiresAt == nil || s.now().After(*identity.HandshakeExpiresAt) {
		return nil, fmt.Errorf("handshake token has expired")
	}

	// Guard against binding a platform account that is already linked to a
	// different Rail user.
	if existing, err := s.platformRepo.GetByPlatformUser(ctx, platform, senderUserID); err == nil {
		if existing.ID != identity.ID && existing.LinkedAt != nil {
			return nil, fmt.Errorf("this account is already linked to another RAIL user")
		}
	}

	if err := s.platformRepo.CompleteHandshake(ctx, identity.ID, senderUserID); err != nil {
		return nil, fmt.Errorf("complete handshake: %w", err)
	}

	now := s.now()
	identity.PlatformUserID = senderUserID
	identity.LinkedAt = &now
	identity.HandshakeTokenHash = nil
	identity.HandshakeExpiresAt = nil
	return identity, nil
}

// LinkVerified binds a platform identity to a Rail user without a token
// handshake. It is used by chat-first onboarding once possession of the phone
// has been proven via SMS OTP, so the sender id is already trustworthy. The
// bind is rejected if the sender is already linked to a different Rail user, and
// is idempotent if the sender is already linked to this same user.
func (s *LinkingService) LinkVerified(ctx context.Context, userID uuid.UUID, platform entities.Platform, senderUserID string) (*entities.PlatformIdentity, error) {
	if senderUserID == "" || strings.HasPrefix(senderUserID, pendingUserIDPrefix) {
		return nil, fmt.Errorf("missing sender identity")
	}

	if existing, err := s.platformRepo.GetByPlatformUser(ctx, platform, senderUserID); err == nil && existing.LinkedAt != nil {
		if existing.UserID == userID {
			return existing, nil
		}
		return nil, fmt.Errorf("this account is already linked to another RAIL user")
	}

	identity, err := s.platformRepo.GetByUserAndPlatform(ctx, userID, platform)
	if err != nil {
		identity = &entities.PlatformIdentity{
			ID:             uuid.New(),
			UserID:         userID,
			Platform:       platform,
			PlatformUserID: senderUserID,
		}
		if err := s.platformRepo.Create(ctx, identity); err != nil {
			return nil, fmt.Errorf("create platform identity: %w", err)
		}
	} else if identity.LinkedAt != nil {
		return identity, nil
	}

	if err := s.platformRepo.CompleteHandshake(ctx, identity.ID, senderUserID); err != nil {
		return nil, fmt.Errorf("complete verified link: %w", err)
	}

	now := s.now()
	identity.PlatformUserID = senderUserID
	identity.LinkedAt = &now
	identity.HandshakeTokenHash = nil
	identity.HandshakeExpiresAt = nil
	return identity, nil
}

func (s *LinkingService) Unlink(ctx context.Context, userID uuid.UUID, platform entities.Platform) error {
	identity, err := s.platformRepo.GetByUserAndPlatform(ctx, userID, platform)
	if err != nil {
		return fmt.Errorf("platform identity not found")
	}
	return s.platformRepo.Delete(ctx, identity.ID)
}

func (s *LinkingService) GetLinkedIdentities(ctx context.Context, userID uuid.UUID) ([]*entities.PlatformIdentity, error) {
	all, err := s.platformRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var linked []*entities.PlatformIdentity
	for _, pi := range all {
		if pi.LinkedAt != nil {
			linked = append(linked, pi)
		}
	}
	return linked, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
