package ratelimit

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

// TieredConfig defines tiered rate limiting configuration
type TieredConfig struct {
	GlobalLimit  int64
	GlobalWindow time.Duration
	IPLimit      int64
	IPWindow     time.Duration
	UserLimit    int64
	UserWindow   time.Duration
	EndpointLimits map[string]EndpointLimit
}

// EndpointLimit defines rate limit for a specific endpoint
type EndpointLimit struct {
	Limit  int64
	Window time.Duration
}

// TieredLimiter implements multi-tier rate limiting
type TieredLimiter struct {
	redis  *redis.Client
	config TieredConfig
	logger *zap.Logger
}

// NewTieredLimiter creates a new tiered rate limiter
func NewTieredLimiter(redis *redis.Client, config TieredConfig, logger *zap.Logger) *TieredLimiter {
	return &TieredLimiter{
		redis:  redis,
		config: config,
		logger: logger,
	}
}

// CheckResult contains the result of a rate limit check
type CheckResult struct {
	Allowed       bool
	Remaining     int64
	ResetAt       time.Time
	RetryAfter    time.Duration
	LimitedBy     string
}

// Check performs tiered rate limit check
func (l *TieredLimiter) Check(ctx context.Context, ip, userID, endpoint string) (*CheckResult, error) {
	// Check global limit
	if l.config.GlobalLimit > 0 {
		allowed, remaining, err := l.checkLimit(ctx, "global", "global", l.config.GlobalLimit, l.config.GlobalWindow)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return &CheckResult{Allowed: false, Remaining: remaining, ResetAt: time.Now().Add(l.config.GlobalWindow), RetryAfter: l.config.GlobalWindow, LimitedBy: "global"}, nil
		}
	}

	// Check IP limit
	if l.config.IPLimit > 0 && ip != "" {
		allowed, remaining, err := l.checkLimit(ctx, "ip", ip, l.config.IPLimit, l.config.IPWindow)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return &CheckResult{Allowed: false, Remaining: remaining, ResetAt: time.Now().Add(l.config.IPWindow), RetryAfter: l.config.IPWindow, LimitedBy: "ip"}, nil
		}
	}

	// Check user limit
	if l.config.UserLimit > 0 && userID != "" {
		allowed, remaining, err := l.checkLimit(ctx, "user", userID, l.config.UserLimit, l.config.UserWindow)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return &CheckResult{Allowed: false, Remaining: remaining, ResetAt: time.Now().Add(l.config.UserWindow), RetryAfter: l.config.UserWindow, LimitedBy: "user"}, nil
		}
	}

	// Check endpoint limit
	if endpointLimit, ok := l.config.EndpointLimits[endpoint]; ok {
		key := fmt.Sprintf("%s:%s", endpoint, ip)
		if userID != "" {
			key = fmt.Sprintf("%s:%s", endpoint, userID)
		}
		allowed, remaining, err := l.checkLimit(ctx, "endpoint", key, endpointLimit.Limit, endpointLimit.Window)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return &CheckResult{Allowed: false, Remaining: remaining, ResetAt: time.Now().Add(endpointLimit.Window), RetryAfter: endpointLimit.Window, LimitedBy: "endpoint"}, nil
		}
	}

	return &CheckResult{Allowed: true, Remaining: -1}, nil
}

func (l *TieredLimiter) checkLimit(ctx context.Context, tier, key string, limit int64, window time.Duration) (bool, int64, error) {
	redisKey := fmt.Sprintf("ratelimit:%s:%s", tier, key)
	now := time.Now()
	windowStart := now.Add(-window)

	pipe := l.redis.Pipeline()
	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", windowStart.UnixNano()))
	countCmd := pipe.ZCount(ctx, redisKey, fmt.Sprintf("%d", windowStart.UnixNano()), "+inf")
	pipe.ZAdd(ctx, redisKey, &redis.Z{Score: float64(now.UnixNano()), Member: now.UnixNano()})
	pipe.Expire(ctx, redisKey, window*2)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	count := countCmd.Val()
	remaining := limit - count - 1
	if remaining < 0 {
		remaining = 0
	}

	return count < limit, remaining, nil
}

// LoginAttemptTracker tracks failed login attempts with exponential backoff
type LoginAttemptTracker struct {
	redis            *redis.Client
	logger           *zap.Logger
	maxAttempts      int
	baseBackoff      time.Duration
	maxBackoff       time.Duration
	captchaThreshold int
}

// NewLoginAttemptTracker creates a new login attempt tracker
func NewLoginAttemptTracker(redis *redis.Client, logger *zap.Logger) *LoginAttemptTracker {
	return &LoginAttemptTracker{
		redis:            redis,
		logger:           logger,
		maxAttempts:      10,
		baseBackoff:      time.Second * 5,
		maxBackoff:       time.Hour,
		captchaThreshold: 3,
	}
}

// LoginAttemptResult contains the result of a login attempt check
type LoginAttemptResult struct {
	Allowed        bool
	FailedAttempts int
	LockedUntil    *time.Time
	RequireCaptcha bool
	RetryAfter     time.Duration
}

// CheckLoginAllowed checks if login is allowed for an identifier
func (t *LoginAttemptTracker) CheckLoginAllowed(ctx context.Context, identifier string) (*LoginAttemptResult, error) {
	key := "login:attempts:" + identifier
	lockKey := "login:locked:" + identifier

	lockTTL, err := t.redis.TTL(ctx, lockKey).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to check lock status: %w", err)
	}
	if lockTTL > 0 {
		lockedUntil := time.Now().Add(lockTTL)
		return &LoginAttemptResult{Allowed: false, LockedUntil: &lockedUntil, RetryAfter: lockTTL}, nil
	}

	attempts, err := t.redis.Get(ctx, key).Int()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to get attempts: %w", err)
	}

	return &LoginAttemptResult{Allowed: true, FailedAttempts: attempts, RequireCaptcha: attempts >= t.captchaThreshold}, nil
}

// RecordFailedAttempt records a failed login attempt
func (t *LoginAttemptTracker) RecordFailedAttempt(ctx context.Context, identifier string) (*LoginAttemptResult, error) {
	key := "login:attempts:" + identifier
	lockKey := "login:locked:" + identifier

	attempts, err := t.redis.Incr(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to increment attempts: %w", err)
	}

	t.redis.Expire(ctx, key, time.Hour)

	result := &LoginAttemptResult{Allowed: true, FailedAttempts: int(attempts), RequireCaptcha: int(attempts) >= t.captchaThreshold}

	if int(attempts) >= t.maxAttempts {
		exponent := int(attempts) - t.maxAttempts
		backoff := time.Duration(float64(t.baseBackoff) * math.Pow(2, float64(exponent)))
		if backoff > t.maxBackoff {
			backoff = t.maxBackoff
		}

		t.redis.Set(ctx, lockKey, "1", backoff)
		lockedUntil := time.Now().Add(backoff)
		result.Allowed = false
		result.LockedUntil = &lockedUntil
		result.RetryAfter = backoff

		t.logger.Warn("Account locked", zap.String("identifier", identifier), zap.Int64("attempts", attempts), zap.Duration("lockout", backoff))
	}

	return result, nil
}

// RecordSuccessfulLogin clears failed attempts
func (t *LoginAttemptTracker) RecordSuccessfulLogin(ctx context.Context, identifier string) error {
	key := "login:attempts:" + identifier
	lockKey := "login:locked:" + identifier

	pipe := t.redis.Pipeline()
	pipe.Del(ctx, key)
	pipe.Del(ctx, lockKey)
	_, err := pipe.Exec(ctx)
	return err
}

// GetCaptchaRequirement checks if CAPTCHA is required
func (t *LoginAttemptTracker) GetCaptchaRequirement(ctx context.Context, identifier string) (bool, error) {
	key := "login:attempts:" + identifier
	attempts, err := t.redis.Get(ctx, key).Int()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return attempts >= t.captchaThreshold, nil
}
