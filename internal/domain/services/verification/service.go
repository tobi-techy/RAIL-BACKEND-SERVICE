package verification

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
)

const (
	verificationCodeLength  = 6
	verificationCodeTTL     = 10 * time.Minute
	maxVerificationAttempts = 3
	rateLimitWindow         = 1 * time.Minute
	maxSendAttempts         = 3 // max OTPs per minute
	maxSendAttemptsHourly   = 8 // max OTPs per hour
	maxSendAttemptsDaily    = 15 // max OTPs per 24h
	minResendCooldown       = 30 * time.Second // minimum gap between consecutive sends
	sendOperationTimeout    = 15 * time.Second
	redisOperationTimeout   = 2 * time.Second
	sendWorkerCount         = 2
	sendQueueSize           = 512
	sendQueueEnqueueWait    = 50 * time.Millisecond
	sendRetryCount          = 2
	sendRetryBackoff        = 500 * time.Millisecond
)

// VerificationService defines the interface for managing verification codes
type VerificationService interface {
	GenerateAndSendCode(ctx context.Context, identifierType, identifier string) (string, error)
	VerifyCode(ctx context.Context, identifierType, identifier, code string) (bool, error)
	CanResendCode(ctx context.Context, identifierType, identifier string) (bool, error)
	RecordSendAttempt(ctx context.Context, identifierType, identifier string) error
}

// verificationService implements VerificationService
type VerificationEmailSender interface {
	SendVerificationEmail(ctx context.Context, email, code string) error
}

type VerificationSMSSender interface {
	SendVerificationSMS(ctx context.Context, phone, code string) error
}

type verificationService struct {
	redisClient cache.RedisClient
	emailSender VerificationEmailSender
	smsSender   VerificationSMSSender
	logger      *zap.Logger
	config      *config.Config
	sendQueue   chan sendRequest
}

type sendRequest struct {
	identifierType string
	identifier     string
	code           string
}

// NewVerificationService creates a new VerificationService
func NewVerificationService(
	redisClient cache.RedisClient,
	emailSender VerificationEmailSender,
	smsSender VerificationSMSSender,
	logger *zap.Logger,
	cfg *config.Config,
) VerificationService {
	svc := &verificationService{
		redisClient: redisClient,
		emailSender: emailSender,
		smsSender:   smsSender,
		logger:      logger,
		config:      cfg,
		sendQueue:   make(chan sendRequest, sendQueueSize),
	}

	for i := 0; i < sendWorkerCount; i++ {
		go svc.sendWorker(i)
	}

	return svc
}

// GenerateAndSendCode generates a 6-digit code, stores it in Redis, and sends it via email or SMS
func (s *verificationService) GenerateAndSendCode(ctx context.Context, identifierType, identifier string) (string, error) {
	opCtx, cancel := withTimeout(ctx, sendOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	// Check rate limits: cooldown → per-minute → per-hour → per-day
	if err := s.checkSendRateLimits(opCtx, identifierType, identifier); err != nil {
		return "", err
	}

	code, err := generateNumericCode(verificationCodeLength)
	if err != nil {
		s.logger.Error("Failed to generate verification code", zap.Error(err))
		return "", fmt.Errorf("failed to generate verification code: %w", err)
	}

	if isDevEnvironment(s.config.Environment) {
		s.logger.Info("DEV MODE: Verification code generated",
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier),
			zap.String("code", code))
	}

	verificationData := entities.VerificationCodeData{
		Code:      code,
		Attempts:  0,
		ExpiresAt: time.Now().Add(verificationCodeTTL),
		CreatedAt: time.Now(),
	}

	key := fmt.Sprintf("verification:%s:%s", identifierType, identifier)
	if err := s.redisClient.Set(opCtx, key, verificationData, verificationCodeTTL); err != nil {
		s.logger.Error("Failed to store verification code in Redis", zap.Error(err), zap.String("key", key))
		return "", fmt.Errorf("failed to store verification code: %w", err)
	}

	// Fail fast if delivery infrastructure is not configured for this identifier type.
	if err := s.validateDelivery(identifierType); err != nil {
		return "", err
	}

	req := sendRequest{
		identifierType: identifierType,
		identifier:     identifier,
		code:           code,
	}

	if err := s.enqueueSend(opCtx, req); err != nil {
		s.logger.Warn("Verification send queue full, falling back to synchronous send",
			zap.Error(err),
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier))

		sendCtx, cancel := withTimeout(opCtx, sendOperationTimeout)
		defer cancel()

		if sendErr := s.sendCode(sendCtx, req); sendErr != nil {
			if isDevEnvironment(s.config.Environment) {
				s.logger.Warn("DEV MODE: Failed to send verification code, using locally generated code",
					zap.String("identifier_type", identifierType),
					zap.String("identifier", identifier),
					zap.String("code", code),
					zap.Error(sendErr))
				return code, nil
			}
			s.logger.Error("Failed to send verification code", zap.Error(sendErr), zap.String("identifier", identifier))
			return "", fmt.Errorf("failed to send verification code: %w", sendErr)
		}
	}

	s.logger.Info("Verification code generated and queued", zap.String("identifier", identifier), zap.String("code", code))
	return code, nil
}

func isDevEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "dev", "development", "local", "test", "testing":
		return true
	default:
		return false
	}
}

// VerifyCode validates the provided code against the stored one
func (s *verificationService) VerifyCode(ctx context.Context, identifierType, identifier, code string) (bool, error) {
	opCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	key := fmt.Sprintf("verification:%s:%s", identifierType, identifier)
	var storedData entities.VerificationCodeData
	err := s.redisClient.Get(opCtx, key, &storedData)
	if err != nil {
		if err.Error() == fmt.Sprintf("key '%s' not found: redis: nil", key) { // Specific check for redis.Nil
			s.logger.Warn("Verification code not found or expired", zap.String("identifier", identifier))
			return false, fmt.Errorf("verification code not found or expired")
		}
		s.logger.Error("Failed to retrieve verification code from Redis", zap.Error(err), zap.String("key", key))
		return false, fmt.Errorf("failed to retrieve verification code: %w", err)
	}

	// Code is valid, delete it from Redis.
	if storedData.Code == code {
		if err := s.redisClient.Del(opCtx, key); err != nil {
			s.logger.Error("Failed to delete verification code from Redis after successful verification", zap.Error(err), zap.String("key", key))
			// Non-critical error, but log it
		}

		s.logger.Info("Verification successful", zap.String("identifier", identifier))
		return true, nil
	}

	// Track only failed attempts.
	storedData.Attempts++
	if storedData.Attempts > maxVerificationAttempts {
		s.logger.Warn("Too many verification attempts for code", zap.String("identifier", identifier))
		_ = s.redisClient.Del(opCtx, key) // Invalidate code after too many attempts
		return false, fmt.Errorf("too many verification attempts. Please request a new code")
	}

	ttl := time.Until(storedData.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Second
	}
	if err := s.redisClient.Set(opCtx, key, storedData, ttl); err != nil {
		s.logger.Error("Failed to update verification code attempts in Redis", zap.Error(err), zap.String("key", key))
		// Non-critical error, continue with verification
	}

	s.logger.Warn("Invalid verification code provided", zap.String("identifier", identifier), zap.Int("attempts", storedData.Attempts))
	return false, fmt.Errorf("invalid verification code")
}

// CanResendCode checks if a new verification code can be sent based on rate limits
func (s *verificationService) CanResendCode(ctx context.Context, identifierType, identifier string) (bool, error) {
	opCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	// Check cooldown
	cooldownKey := fmt.Sprintf("otp_cooldown:%s:%s", identifierType, identifier)
	if exists, _ := s.redisClient.Exists(opCtx, cooldownKey); exists {
		return false, nil
	}

	// Check per-minute limit
	minuteKey := fmt.Sprintf("send_attempts:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(opCtx, minuteKey); count >= maxSendAttempts {
		return false, nil
	}

	// Check hourly limit
	hourlyKey := fmt.Sprintf("send_attempts_hourly:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(opCtx, hourlyKey); count >= maxSendAttemptsHourly {
		return false, nil
	}

	// Check daily limit
	dailyKey := fmt.Sprintf("send_attempts_daily:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(opCtx, dailyKey); count >= maxSendAttemptsDaily {
		return false, nil
	}

	return true, nil
}

// RecordSendAttempt records a send attempt for rate limiting
func (s *verificationService) RecordSendAttempt(ctx context.Context, identifierType, identifier string) error {
	opCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	s.incrWithTTL(opCtx, fmt.Sprintf("send_attempts:%s:%s", identifierType, identifier), rateLimitWindow)
	s.incrWithTTL(opCtx, fmt.Sprintf("send_attempts_hourly:%s:%s", identifierType, identifier), time.Hour)
	s.incrWithTTL(opCtx, fmt.Sprintf("send_attempts_daily:%s:%s", identifierType, identifier), 24*time.Hour)
	_ = s.redisClient.Set(opCtx, fmt.Sprintf("otp_cooldown:%s:%s", identifierType, identifier), "1", minResendCooldown)
	return nil
}

// checkSendRateLimits enforces cooldown, per-minute, hourly, and daily OTP send limits.
func (s *verificationService) checkSendRateLimits(ctx context.Context, identifierType, identifier string) error {
	// 1. Cooldown between consecutive sends
	cooldownKey := fmt.Sprintf("otp_cooldown:%s:%s", identifierType, identifier)
	if exists, _ := s.redisClient.Exists(ctx, cooldownKey); exists {
		s.logger.Warn("OTP cooldown active", zap.String("identifier", identifier))
		return fmt.Errorf("please wait %s before requesting another code", minResendCooldown)
	}

	// 2. Per-minute limit
	minuteKey := fmt.Sprintf("send_attempts:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(ctx, minuteKey); count >= maxSendAttempts {
		s.logger.Warn("Per-minute OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("too many verification code send attempts. Please try again after %s", rateLimitWindow)
	}

	// 3. Hourly limit
	hourlyKey := fmt.Sprintf("send_attempts_hourly:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(ctx, hourlyKey); count >= maxSendAttemptsHourly {
		s.logger.Warn("Hourly OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("too many verification codes sent this hour. Please try again later")
	}

	// 4. Daily limit
	dailyKey := fmt.Sprintf("send_attempts_daily:%s:%s", identifierType, identifier)
	if count, _ := s.getCounter(ctx, dailyKey); count >= maxSendAttemptsDaily {
		s.logger.Warn("Daily OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("daily verification code limit reached. Please try again tomorrow")
	}

	// All checks passed — record the attempt across all windows
	s.incrWithTTL(ctx, minuteKey, rateLimitWindow)
	s.incrWithTTL(ctx, hourlyKey, time.Hour)
	s.incrWithTTL(ctx, dailyKey, 24*time.Hour)
	_ = s.redisClient.Set(ctx, cooldownKey, "1", minResendCooldown)

	return nil
}

func (s *verificationService) getCounter(ctx context.Context, key string) (int64, error) {
	var val string
	if err := s.redisClient.Get(ctx, key, &val); err != nil {
		return 0, nil // key doesn't exist = 0 attempts
	}
	var n int64
	fmt.Sscanf(val, "%d", &n)
	return n, nil
}

func (s *verificationService) incrWithTTL(ctx context.Context, key string, ttl time.Duration) {
	count, err := s.redisClient.Incr(ctx, key)
	if err != nil {
		s.logger.Error("Failed to increment rate limit counter", zap.Error(err), zap.String("key", key))
		return
	}
	if count == 1 {
		_ = s.redisClient.Expire(ctx, key, ttl)
	}
}

func withTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func (s *verificationService) validateDelivery(identifierType string) error {
	switch identifierType {
	case "email":
		if s.emailSender == nil && !isDevEnvironment(s.config.Environment) {
			return fmt.Errorf("email service not configured")
		}
	case "phone":
		if s.smsSender == nil {
			return fmt.Errorf("sms service not configured")
		}
	default:
		return fmt.Errorf("unsupported identifier type: %s", identifierType)
	}
	return nil
}

func (s *verificationService) enqueueSend(ctx context.Context, req sendRequest) error {
	queueCtx, cancel := withTimeout(ctx, sendQueueEnqueueWait)
	defer cancel()

	select {
	case s.sendQueue <- req:
		return nil
	case <-queueCtx.Done():
		return fmt.Errorf("verification send queue timeout: %w", queueCtx.Err())
	}
}

func (s *verificationService) sendWorker(workerID int) {
	for req := range s.sendQueue {
		var lastErr error
		for attempt := 1; attempt <= sendRetryCount; attempt++ {
			attemptCtx, cancel := context.WithTimeout(context.Background(), sendOperationTimeout)
			lastErr = s.sendCode(attemptCtx, req)
			cancel()
			if lastErr == nil {
				s.logger.Debug("Verification code delivered",
					zap.Int("worker_id", workerID),
					zap.Int("attempt", attempt),
					zap.String("identifier_type", req.identifierType),
					zap.String("identifier", req.identifier))
				break
			}

			if attempt < sendRetryCount {
				time.Sleep(sendRetryBackoff * time.Duration(attempt))
			}
		}

		if lastErr != nil {
			if isDevEnvironment(s.config.Environment) {
				s.logger.Warn("DEV MODE: verification code dispatch failed in worker",
					zap.Int("worker_id", workerID),
					zap.String("identifier_type", req.identifierType),
					zap.String("identifier", req.identifier),
					zap.String("code", req.code),
					zap.Error(lastErr))
				continue
			}

			s.logger.Error("Verification code dispatch failed in worker",
				zap.Int("worker_id", workerID),
				zap.String("identifier_type", req.identifierType),
				zap.String("identifier", req.identifier),
				zap.Error(lastErr))
		}
	}
}

func (s *verificationService) sendCode(ctx context.Context, req sendRequest) error {
	switch req.identifierType {
	case "email":
		if s.emailSender == nil {
			if isDevEnvironment(s.config.Environment) {
				return nil
			}
			return fmt.Errorf("email service not configured")
		}
		return s.emailSender.SendVerificationEmail(ctx, req.identifier, req.code)
	case "phone":
		if s.smsSender == nil {
			return fmt.Errorf("sms service not configured")
		}
		return s.smsSender.SendVerificationSMS(ctx, req.identifier, req.code)
	default:
		return fmt.Errorf("unsupported identifier type: %s", req.identifierType)
	}
}

// generateNumericCode generates a random numeric string of specified length
func generateNumericCode(length int) (string, error) {
	const digits = "0123456789"
	b := make([]byte, length)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		b[i] = digits[num.Int64()]
	}
	return string(b), nil
}

func normalizeVerificationIdentifier(identifierType, identifier string) string {
	normalized := strings.TrimSpace(identifier)
	if identifierType == "email" {
		return strings.ToLower(normalized)
	}
	return normalized
}
