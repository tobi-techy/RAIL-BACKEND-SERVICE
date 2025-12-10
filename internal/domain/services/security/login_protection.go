package security

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

const (
	loginAttemptsPrefix    = "login_attempts:"
	accountLockPrefix      = "account_lock:"
	defaultMaxAttempts     = 5
	defaultLockDuration    = 15 * time.Minute
	defaultAttemptWindow   = 15 * time.Minute
)

type LoginProtectionService struct {
	redis        *redis.Client
	logger       *zap.Logger
	maxAttempts  int
	lockDuration time.Duration
}

type LoginAttemptResult struct {
	Allowed           bool
	RemainingAttempts int
	LockedUntil       *time.Time
	Reason            string
}

func NewLoginProtectionService(redisClient *redis.Client, logger *zap.Logger) *LoginProtectionService {
	return &LoginProtectionService{
		redis:        redisClient,
		logger:       logger,
		maxAttempts:  defaultMaxAttempts,
		lockDuration: defaultLockDuration,
	}
}

// CheckLoginAllowed verifies if login attempt is allowed
func (s *LoginProtectionService) CheckLoginAllowed(ctx context.Context, identifier string) (*LoginAttemptResult, error) {
	lockKey := accountLockPrefix + identifier

	// Check if account is locked
	lockTTL, err := s.redis.TTL(ctx, lockKey).Result()
	if err != nil && err != redis.Nil {
		s.logger.Error("Failed to check account lock", zap.Error(err))
		return &LoginAttemptResult{Allowed: true}, nil // Fail open
	}

	if lockTTL > 0 {
		lockedUntil := time.Now().Add(lockTTL)
		return &LoginAttemptResult{
			Allowed:     false,
			LockedUntil: &lockedUntil,
			Reason:      "Account temporarily locked due to too many failed attempts",
		}, nil
	}

	// Get current attempt count
	attemptsKey := loginAttemptsPrefix + identifier
	attempts, _ := s.redis.Get(ctx, attemptsKey).Int()
	remaining := s.maxAttempts - attempts

	return &LoginAttemptResult{
		Allowed:           true,
		RemainingAttempts: remaining,
	}, nil
}

// RecordFailedAttempt records a failed login and returns if account should be locked
func (s *LoginProtectionService) RecordFailedAttempt(ctx context.Context, identifier, ipAddress, userAgent string) (*LoginAttemptResult, error) {
	attemptsKey := loginAttemptsPrefix + identifier

	// Increment attempts
	attempts, err := s.redis.Incr(ctx, attemptsKey).Result()
	if err != nil {
		s.logger.Error("Failed to record login attempt", zap.Error(err))
		return &LoginAttemptResult{Allowed: true}, nil
	}

	// Set expiry on first attempt
	if attempts == 1 {
		s.redis.Expire(ctx, attemptsKey, defaultAttemptWindow)
	}

	remaining := s.maxAttempts - int(attempts)
	if remaining < 0 {
		remaining = 0
	}

	// Lock account if max attempts exceeded
	if attempts >= int64(s.maxAttempts) {
		lockKey := accountLockPrefix + identifier
		s.redis.Set(ctx, lockKey, "locked", s.lockDuration)
		s.redis.Del(ctx, attemptsKey)

		lockedUntil := time.Now().Add(s.lockDuration)

		s.logger.Warn("Account locked due to failed attempts",
			zap.String("identifier", identifier),
			zap.String("ip", ipAddress),
			zap.Int64("attempts", attempts))

		return &LoginAttemptResult{
			Allowed:           false,
			RemainingAttempts: 0,
			LockedUntil:       &lockedUntil,
			Reason:            "Account locked due to too many failed attempts",
		}, nil
	}

	return &LoginAttemptResult{
		Allowed:           true,
		RemainingAttempts: remaining,
	}, nil
}

// ClearFailedAttempts clears failed attempts after successful login
func (s *LoginProtectionService) ClearFailedAttempts(ctx context.Context, identifier string) error {
	attemptsKey := loginAttemptsPrefix + identifier
	return s.redis.Del(ctx, attemptsKey).Err()
}

// UnlockAccount manually unlocks an account (admin function)
func (s *LoginProtectionService) UnlockAccount(ctx context.Context, identifier string) error {
	lockKey := accountLockPrefix + identifier
	attemptsKey := loginAttemptsPrefix + identifier

	pipe := s.redis.Pipeline()
	pipe.Del(ctx, lockKey)
	pipe.Del(ctx, attemptsKey)
	_, err := pipe.Exec(ctx)

	if err == nil {
		s.logger.Info("Account manually unlocked", zap.String("identifier", identifier))
	}
	return err
}

// GetLockStatus returns current lock status for an account
func (s *LoginProtectionService) GetLockStatus(ctx context.Context, identifier string) (bool, *time.Time, error) {
	lockKey := accountLockPrefix + identifier
	ttl, err := s.redis.TTL(ctx, lockKey).Result()
	if err != nil {
		return false, nil, err
	}

	if ttl > 0 {
		lockedUntil := time.Now().Add(ttl)
		return true, &lockedUntil, nil
	}

	return false, nil, nil
}

// RecordSuspiciousActivity logs suspicious login patterns
func (s *LoginProtectionService) RecordSuspiciousActivity(ctx context.Context, identifier, activityType, ipAddress string, metadata map[string]interface{}) {
	s.logger.Warn("Suspicious login activity detected",
		zap.String("identifier", identifier),
		zap.String("activity_type", activityType),
		zap.String("ip_address", ipAddress),
		zap.Any("metadata", metadata))

	// Store for analysis
	key := fmt.Sprintf("suspicious_activity:%s:%d", identifier, time.Now().Unix())
	s.redis.HSet(ctx, key, map[string]interface{}{
		"type":       activityType,
		"ip":         ipAddress,
		"timestamp":  time.Now().Unix(),
	})
	s.redis.Expire(ctx, key, 24*time.Hour)
}
