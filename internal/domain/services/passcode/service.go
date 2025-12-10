package passcode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/crypto"
)

const (
	defaultMaxAttempts     = 5
	defaultLockDuration    = 15 * time.Minute
	defaultSessionTTL      = 10 * time.Minute
	passcodeMinLength      = 4
	passcodeMaxLength      = 4
	passcodeRedisNamespace = "passcode_session"
)

var (
	// ErrPasscodeAlreadySet is returned when attempting to create a passcode that already exists
	ErrPasscodeAlreadySet = errors.New("passcode already set")
	// ErrPasscodeNotSet is returned when operations require a passcode but none exists
	ErrPasscodeNotSet = errors.New("passcode not configured")
	// ErrPasscodeMismatch is returned when the supplied passcode is incorrect
	ErrPasscodeMismatch = errors.New("passcode is incorrect")
	// ErrPasscodeLocked is returned when the passcode is temporarily locked due to failed attempts
	ErrPasscodeLocked = errors.New("passcode locked due to too many failed attempts")
	// ErrPasscodeInvalidFormat is returned when the passcode does not meet format requirements
	ErrPasscodeInvalidFormat = errors.New("passcode must be 4 digits")
	// ErrPasscodeSameAsCurrent is returned when the new passcode matches the existing one
	ErrPasscodeSameAsCurrent = errors.New("new passcode must be different from the current passcode")
)

// UserPasscodeRepository defines the data access required by the passcode service
type UserPasscodeRepository interface {
	GetPasscodeMetadata(ctx context.Context, userID uuid.UUID) (*entities.PasscodeMetadata, error)
	UpdatePasscodeHash(ctx context.Context, userID uuid.UUID, hash string, updatedAt time.Time) error
	ResetPasscodeFailures(ctx context.Context, userID uuid.UUID) error
	IncrementPasscodeFailures(ctx context.Context, userID uuid.UUID, failureCountThreshold int, lockUntil *time.Time) (*entities.PasscodeMetadata, error)
	ClearPasscode(ctx context.Context, userID uuid.UUID) error
}

// Service encapsulates passcode management logic
type Service struct {
	userRepo     UserPasscodeRepository
	redis        cache.RedisClient
	logger       *zap.Logger
	maxAttempts  int
	lockDuration time.Duration
	sessionTTL   time.Duration
}

// NewService constructs a new passcode service with defaults
func NewService(
	userRepo UserPasscodeRepository,
	redis cache.RedisClient,
	logger *zap.Logger,
) *Service {
	return &Service{
		userRepo:     userRepo,
		redis:        redis,
		logger:       logger,
		maxAttempts:  defaultMaxAttempts,
		lockDuration: defaultLockDuration,
		sessionTTL:   defaultSessionTTL,
	}
}

// GetStatus returns the current passcode configuration for a user
func (s *Service) GetStatus(ctx context.Context, userID uuid.UUID) (*entities.PasscodeStatusResponse, error) {
	meta, err := s.userRepo.GetPasscodeMetadata(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load passcode metadata: %w", err)
	}

	enabled := meta.HashedPasscode != nil && *meta.HashedPasscode != ""
	locked := isLocked(meta)

	status := &entities.PasscodeStatusResponse{
		Enabled:           enabled,
		Locked:            locked,
		FailedAttempts:    meta.FailedAttempts,
		RemainingAttempts: s.remainingAttempts(meta.FailedAttempts),
		LockedUntil:       meta.LockedUntil,
		UpdatedAt:         meta.UpdatedAt,
	}

	return status, nil
}

// SetPasscode configures a passcode for the user when one does not exist
func (s *Service) SetPasscode(ctx context.Context, userID uuid.UUID, passcode string) (*entities.PasscodeStatusResponse, error) {
	if err := validatePasscode(passcode); err != nil {
		return nil, err
	}

	meta, err := s.userRepo.GetPasscodeMetadata(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch passcode metadata: %w", err)
	}
	if meta.HashedPasscode != nil && *meta.HashedPasscode != "" {
		return nil, ErrPasscodeAlreadySet
	}

	hash, err := crypto.HashPassword(passcode)
	if err != nil {
		return nil, fmt.Errorf("failed to hash passcode: %w", err)
	}

	now := time.Now()
	if err := s.userRepo.UpdatePasscodeHash(ctx, userID, hash, now); err != nil {
		return nil, fmt.Errorf("failed to persist passcode: %w", err)
	}

	s.logger.Info("Passcode configured successfully", zap.String("user_id", userID.String()))
	return s.GetStatus(ctx, userID)
}

// UpdatePasscode rotates an existing passcode after validating the current one
func (s *Service) UpdatePasscode(ctx context.Context, userID uuid.UUID, current, newPasscode string) (*entities.PasscodeStatusResponse, error) {
	if err := validatePasscode(newPasscode); err != nil {
		return nil, err
	}

	meta, err := s.userRepo.GetPasscodeMetadata(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch passcode metadata: %w", err)
	}

	if meta.HashedPasscode == nil || *meta.HashedPasscode == "" {
		return nil, ErrPasscodeNotSet
	}

	if isLocked(meta) {
		return nil, ErrPasscodeLocked
	}

	if !crypto.ValidatePassword(current, *meta.HashedPasscode) {
		if _, incErr := s.recordFailedAttempt(ctx, userID, meta); incErr != nil {
			s.logger.Warn("Failed to record passcode failure during update", zap.Error(incErr))
		}
		return nil, ErrPasscodeMismatch
	}

	if crypto.ValidatePassword(newPasscode, *meta.HashedPasscode) {
		return nil, ErrPasscodeSameAsCurrent
	}

	hash, err := crypto.HashPassword(newPasscode)
	if err != nil {
		return nil, fmt.Errorf("failed to hash new passcode: %w", err)
	}

	now := time.Now()
	if err := s.userRepo.UpdatePasscodeHash(ctx, userID, hash, now); err != nil {
		return nil, fmt.Errorf("failed to update passcode: %w", err)
	}

	if err := s.userRepo.ResetPasscodeFailures(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset passcode failure counters after update",
			zap.Error(err),
			zap.String("user_id", userID.String()))
	}

	s.logger.Info("Passcode updated successfully", zap.String("user_id", userID.String()))
	return s.GetStatus(ctx, userID)
}

// VerifyPasscode validates the supplied passcode and creates a session token for sensitive operations
func (s *Service) VerifyPasscode(ctx context.Context, userID uuid.UUID, passcode string) (string, time.Time, error) {
	meta, err := s.userRepo.GetPasscodeMetadata(ctx, userID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to fetch passcode metadata: %w", err)
	}

	if meta.HashedPasscode == nil || *meta.HashedPasscode == "" {
		return "", time.Time{}, ErrPasscodeNotSet
	}

	if isLocked(meta) {
		return "", time.Time{}, ErrPasscodeLocked
	}

	if !crypto.ValidatePassword(passcode, *meta.HashedPasscode) {
		newMeta, incErr := s.recordFailedAttempt(ctx, userID, meta)
		if incErr != nil {
			s.logger.Warn("Failed to record passcode failure during verification",
				zap.Error(incErr),
				zap.String("user_id", userID.String()))
		} else if isLocked(newMeta) {
			return "", time.Time{}, ErrPasscodeLocked
		}
		return "", time.Time{}, ErrPasscodeMismatch
	}

	if err := s.userRepo.ResetPasscodeFailures(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset passcode failure counters after verification",
			zap.Error(err),
			zap.String("user_id", userID.String()))
	}

	// Create a short-lived session token for sensitive operations
	session, token, err := s.createSession(ctx, userID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create passcode session: %w", err)
	}

	s.logger.Info("Passcode verified successfully and session token created",
		zap.String("user_id", userID.String()),
		zap.Time("session_expires_at", session.ExpiresAt))

	return token, session.ExpiresAt, nil
}

// RemovePasscode disables the passcode requirement after validating the current passcode
func (s *Service) RemovePasscode(ctx context.Context, userID uuid.UUID, passcode string) (*entities.PasscodeStatusResponse, error) {
	meta, err := s.userRepo.GetPasscodeMetadata(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch passcode metadata: %w", err)
	}

	if meta.HashedPasscode == nil || *meta.HashedPasscode == "" {
		return nil, ErrPasscodeNotSet
	}

	if isLocked(meta) {
		return nil, ErrPasscodeLocked
	}

	if !crypto.ValidatePassword(passcode, *meta.HashedPasscode) {
		if _, incErr := s.recordFailedAttempt(ctx, userID, meta); incErr != nil {
			s.logger.Warn("Failed to record passcode failure during removal", zap.Error(incErr))
		}
		return nil, ErrPasscodeMismatch
	}

	if err := s.userRepo.ClearPasscode(ctx, userID); err != nil {
		return nil, fmt.Errorf("failed to clear passcode: %w", err)
	}

	s.logger.Info("Passcode removed successfully", zap.String("user_id", userID.String()))
	return s.GetStatus(ctx, userID)
}

// ValidateSession confirms whether a passcode verification token is still valid
func (s *Service) ValidateSession(ctx context.Context, userID uuid.UUID, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	key := s.sessionKey(userID, token)
	exists, err := s.redis.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to validate passcode session: %w", err)
	}
	return exists, nil
}

// InvalidateSession explicitly invalidates a passcode session token
func (s *Service) InvalidateSession(ctx context.Context, userID uuid.UUID, token string) error {
	if token == "" {
		return nil
	}
	key := s.sessionKey(userID, token)
	if err := s.redis.Del(ctx, key); err != nil {
		return fmt.Errorf("failed to invalidate passcode session: %w", err)
	}
	return nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, userID uuid.UUID, meta *entities.PasscodeMetadata) (*entities.PasscodeMetadata, error) {
	nextAttempt := meta.FailedAttempts + 1
	var lockUntil *time.Time

	if nextAttempt >= s.maxAttempts {
		lock := time.Now().Add(s.lockDuration)
		lockUntil = &lock
	}

	updatedMeta, err := s.userRepo.IncrementPasscodeFailures(ctx, userID, s.maxAttempts, lockUntil)
	if err != nil {
		return meta, err
	}

	return updatedMeta, nil
}

func (s *Service) createSession(ctx context.Context, userID uuid.UUID) (*entities.PasscodeSession, string, error) {
	token, err := crypto.GenerateSecureToken()
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	session := &entities.PasscodeSession{
		UserID:    userID,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.sessionTTL),
	}

	key := s.sessionKey(userID, token)
	if err := s.redis.Set(ctx, key, session, s.sessionTTL); err != nil {
		return nil, "", err
	}

	return session, token, nil
}

func (s *Service) sessionKey(userID uuid.UUID, token string) string {
	return fmt.Sprintf("%s:%s:%s", passcodeRedisNamespace, userID.String(), token)
}

func (s *Service) remainingAttempts(failed int) int {
	remaining := s.maxAttempts - failed
	if remaining < 0 {
		return 0
	}
	return remaining
}

func validatePasscode(passcode string) error {
	passcode = strings.TrimSpace(passcode)
	length := len(passcode)
	if length < passcodeMinLength || length > passcodeMaxLength {
		return ErrPasscodeInvalidFormat
	}

	for _, r := range passcode {
		if !unicode.IsDigit(r) {
			return ErrPasscodeInvalidFormat
		}
	}
	return nil
}

func isLocked(meta *entities.PasscodeMetadata) bool {
	if meta == nil || meta.LockedUntil == nil {
		return false
	}
	return meta.LockedUntil.After(time.Now())
}
