package verification

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
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
	// GenerateAndSendCodeSync sends inline and reports the real delivery
	// outcome instead of "queued". simulated=true means the code was stored
	// but deliberately NOT delivered (dev environment without a sender);
	// callers must disclose that to the user rather than claim a send.
	GenerateAndSendCodeSync(ctx context.Context, identifierType, identifier string) (code string, simulated bool, err error)
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

	// Make delivery capability visible at startup: a nil sender with a
	// dev-classified environment silently simulates sends, which is exactly
	// the misconfiguration that looks like "OTP claimed sent, nothing arrived".
	logger.Info("verification delivery config",
		zap.Bool("email_configured", emailSender != nil),
		zap.Bool("sms_configured", smsSender != nil),
		zap.String("environment", cfg.Environment),
	)

	return svc
}

// GenerateAndSendCode generates a 6-digit code, stores it in Redis, and sends it via email or SMS
func (s *verificationService) GenerateAndSendCode(ctx context.Context, identifierType, identifier string) (string, error) {
	opCtx, cancel := withTimeout(ctx, sendOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	// A recipient every provider has permanently refused is answered before the
	// rate limiter and before anything is enqueued. This is what makes the
	// app-facing (async) path honest: it normally returns "queued" the moment the
	// send is enqueued, so without this the caller is told a code is on its way
	// while the worker is discovering it can never be delivered.
	if err := s.blockedRecipientError(opCtx, identifierType, identifier); err != nil {
		s.logger.Warn("OTP send skipped: every provider refuses this recipient",
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier))
		return "", err
	}

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

	if err := s.storeCode(opCtx, identifierType, identifier, code); err != nil {
		return "", err
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
			if entities.IsPermanentEmailDeliveryError(sendErr) {
				s.handlePermanentDeliveryFailure(opCtx, identifierType, identifier, sendErr)
			}
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

	s.logger.Info("Verification code generated and queued", zap.String("identifier", identifier))
	return code, nil
}

// GenerateAndSendCodeSync is the honest variant used by conversational
// onboarding: the send happens inline before the caller replies "sent", so a
// provider failure reaches the user instead of dying in the background worker.
// simulated=true means no sender exists for this environment (dev bypass) and
// nothing was delivered; callers must disclose that rather than claim a send.
func (s *verificationService) GenerateAndSendCodeSync(ctx context.Context, identifierType, identifier string) (string, bool, error) {
	opCtx, cancel := withTimeout(ctx, sendOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)

	// Answer a known-blocked recipient before spending budget or a provider call.
	if err := s.blockedRecipientError(opCtx, identifierType, identifier); err != nil {
		s.logger.Warn("OTP send skipped: every provider refuses this recipient",
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier))
		return "", false, err
	}

	if err := s.checkSendRateLimits(opCtx, identifierType, identifier); err != nil {
		return "", false, err
	}

	code, err := generateNumericCode(verificationCodeLength)
	if err != nil {
		s.logger.Error("Failed to generate verification code", zap.Error(err))
		return "", false, fmt.Errorf("failed to generate verification code: %w", err)
	}

	if err := s.storeCode(opCtx, identifierType, identifier, code); err != nil {
		return "", false, err
	}

	if err := s.validateDelivery(identifierType); err != nil {
		return "", false, err
	}

	// Dev without a configured sender: validateDelivery let it through, but
	// nothing would actually go out. Surface that as simulated instead of
	// pretending the message was delivered.
	simulated := s.deliverySimulated(identifierType)
	if simulated {
		s.logger.Info("DEV MODE: verification code simulated (no sender configured)",
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier))
		return code, true, nil
	}

	req := sendRequest{identifierType: identifierType, identifier: identifier, code: code}
	sendCtx, cancel2 := withTimeout(opCtx, sendOperationTimeout)
	defer cancel2()
	if err := s.sendCode(sendCtx, req); err != nil {
		if entities.IsPermanentEmailDeliveryError(err) {
			s.handlePermanentDeliveryFailure(opCtx, identifierType, identifier, err)
		}
		s.logger.Error("Failed to send verification code",
			zap.Error(err),
			zap.String("identifier_type", identifierType),
			zap.String("identifier", identifier))
		return "", false, fmt.Errorf("failed to send verification code: %w", err)
	}
	return code, false, nil
}

func (s *verificationService) storeCode(ctx context.Context, identifierType, identifier, code string) error {
	verificationData := entities.VerificationCodeData{
		Code:      code,
		Attempts:  0,
		ExpiresAt: time.Now().Add(verificationCodeTTL),
		CreatedAt: time.Now(),
	}
	key := fmt.Sprintf("verification:%s:%s", identifierType, identifier)
	if err := s.redisClient.Set(ctx, key, verificationData, verificationCodeTTL); err != nil {
		s.logger.Error("Failed to store verification code in Redis", zap.Error(err), zap.String("key", key))
		return fmt.Errorf("failed to store verification code: %w", err)
	}
	return nil
}

// deliverySimulated reports whether a send for this identifier type would be a
// no-op simulation (dev environment, sender absent). Only call after
// validateDelivery has passed.
func (s *verificationService) deliverySimulated(identifierType string) bool {
	switch identifierType {
	case "email":
		return s.emailSender == nil && isDevEnvironment(s.config.Environment)
	case "phone":
		return s.smsSender == nil && isDevEnvironment(s.config.Environment)
	default:
		return false
	}
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
	if subtle.ConstantTimeCompare([]byte(storedData.Code), []byte(code)) == 1 {
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

// otpAttemptKeys holds the Redis keys that make up one identifier's OTP send
// budget. Named fields (rather than inline Sprintf calls at each site) are what
// keep the refund path in step with the charge path: a key mismatch would
// silently leave an undeliverable address holding the whole budget.
type otpAttemptKeys struct {
	minute   string
	hourly   string
	daily    string
	cooldown string
}

func newOTPAttemptKeys(identifierType, identifier string) otpAttemptKeys {
	return otpAttemptKeys{
		minute:   fmt.Sprintf("send_attempts:%s:%s", identifierType, identifier),
		hourly:   fmt.Sprintf("send_attempts_hourly:%s:%s", identifierType, identifier),
		daily:    fmt.Sprintf("send_attempts_daily:%s:%s", identifierType, identifier),
		cooldown: fmt.Sprintf("otp_cooldown:%s:%s", identifierType, identifier),
	}
}

// CanResendCode checks if a new verification code can be sent based on rate limits
func (s *verificationService) CanResendCode(ctx context.Context, identifierType, identifier string) (bool, error) {
	opCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)
	keys := newOTPAttemptKeys(identifierType, identifier)

	// Check cooldown
	if exists, _ := s.redisClient.Exists(opCtx, keys.cooldown); exists {
		return false, nil
	}

	// Check per-minute limit
	if count, _ := s.getCounter(opCtx, keys.minute); count >= maxSendAttempts {
		return false, nil
	}

	// Check hourly limit
	if count, _ := s.getCounter(opCtx, keys.hourly); count >= maxSendAttemptsHourly {
		return false, nil
	}

	// Check daily limit
	if count, _ := s.getCounter(opCtx, keys.daily); count >= maxSendAttemptsDaily {
		return false, nil
	}

	return true, nil
}

// RecordSendAttempt records a send attempt for rate limiting
func (s *verificationService) RecordSendAttempt(ctx context.Context, identifierType, identifier string) error {
	opCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	identifier = normalizeVerificationIdentifier(identifierType, identifier)
	keys := newOTPAttemptKeys(identifierType, identifier)

	s.incrWithTTL(opCtx, keys.minute, rateLimitWindow)
	s.incrWithTTL(opCtx, keys.hourly, time.Hour)
	s.incrWithTTL(opCtx, keys.daily, 24*time.Hour)
	_ = s.redisClient.Set(opCtx, keys.cooldown, "1", minResendCooldown)
	return nil
}

// checkSendRateLimits enforces cooldown, per-minute, hourly, and daily OTP send limits.
func (s *verificationService) checkSendRateLimits(ctx context.Context, identifierType, identifier string) error {
	keys := newOTPAttemptKeys(identifierType, identifier)

	// 1. Cooldown between consecutive sends
	if exists, _ := s.redisClient.Exists(ctx, keys.cooldown); exists {
		s.logger.Warn("OTP cooldown active", zap.String("identifier", identifier))
		return fmt.Errorf("please wait %s before requesting another code", minResendCooldown)
	}

	// 2. Per-minute limit
	if count, _ := s.getCounter(ctx, keys.minute); count >= maxSendAttempts {
		s.logger.Warn("Per-minute OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("too many verification code send attempts. Please try again after %s", rateLimitWindow)
	}

	// 3. Hourly limit
	if count, _ := s.getCounter(ctx, keys.hourly); count >= maxSendAttemptsHourly {
		s.logger.Warn("Hourly OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("too many verification codes sent this hour. Please try again later")
	}

	// 4. Daily limit
	if count, _ := s.getCounter(ctx, keys.daily); count >= maxSendAttemptsDaily {
		s.logger.Warn("Daily OTP rate limit exceeded", zap.String("identifier", identifier))
		return fmt.Errorf("daily verification code limit reached. Please try again tomorrow")
	}

	// All checks passed — record the attempt across all windows
	s.incrWithTTL(ctx, keys.minute, rateLimitWindow)
	s.incrWithTTL(ctx, keys.hourly, time.Hour)
	s.incrWithTTL(ctx, keys.daily, 24*time.Hour)
	_ = s.redisClient.Set(ctx, keys.cooldown, "1", minResendCooldown)

	return nil
}

// otpUndeliverableTTL bounds how long a permanently refused recipient is
// remembered as undeliverable. It needs to outlast a person retrying within one
// onboarding session (so they are told the truth instead of being queued behind
// a send that cannot succeed) and stay short enough that clearing the
// suppression at the provider takes effect the same day.
const otpUndeliverableTTL = 6 * time.Hour

// undeliverableRecord is the marker payload. It is stored as JSON through the
// Redis client's normal round-trip, so it survives a process restart and is
// readable by whichever replica handles the next attempt.
type undeliverableRecord struct {
	Provider string    `json:"provider"`
	Reason   string    `json:"reason"`
	MarkedAt time.Time `json:"marked_at"`
}

func otpUndeliverableKey(identifierType, identifier string) string {
	return fmt.Sprintf("otp_undeliverable:%s:%s", identifierType, identifier)
}

// blockedRecipientError reports the permanent delivery failure previously
// recorded for this identifier, so callers can refuse the send up front instead
// of discovering it after the fact. Every Redis problem fails open: a marker we
// cannot read must never stop a code that might otherwise arrive.
func (s *verificationService) blockedRecipientError(ctx context.Context, identifierType, identifier string) error {
	readCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	key := otpUndeliverableKey(identifierType, identifier)
	exists, err := s.redisClient.Exists(readCtx, key)
	if err != nil {
		s.logger.Warn("Failed to read undeliverable marker", zap.Error(err), zap.String("identifier", identifier))
		return nil
	}
	if !exists {
		return nil
	}

	var record undeliverableRecord
	if err := s.redisClient.Get(readCtx, key, &record); err != nil {
		s.logger.Warn("Failed to read undeliverable marker payload",
			zap.Error(err), zap.String("identifier", identifier))
		return nil
	}

	// The typed error is what callers switch on, so the same classification flows
	// through both the up-front refusal and a fresh provider rejection.
	return fmt.Errorf("email delivery to %s is blocked: provider %s refuses the address (reason %s, recorded %s): %w",
		identifier,
		record.Provider,
		record.Reason,
		record.MarkedAt.UTC().Format(time.RFC3339),
		&entities.EmailDeliveryError{
			Provider:  record.Provider,
			Recipient: identifier,
			Reason:    record.Reason,
			Permanent: true,
		})
}

// markUndeliverable records that every configured provider permanently refused
// this identifier. It is only ever called once the send has failed for good, so
// a success anywhere clears the need for it.
func (s *verificationService) markUndeliverable(ctx context.Context, identifierType, identifier string, deliveryErr error) {
	writeCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	record := undeliverableRecord{MarkedAt: time.Now().UTC()}
	var typed *entities.EmailDeliveryError
	if errors.As(deliveryErr, &typed) {
		record.Provider = typed.Provider
		record.Reason = typed.Reason
	}

	if err := s.redisClient.Set(writeCtx, otpUndeliverableKey(identifierType, identifier), record, otpUndeliverableTTL); err != nil {
		s.logger.Warn("Failed to record undeliverable recipient",
			zap.Error(err), zap.String("identifier", identifier))
		return
	}

	s.logger.Warn("Recipient marked undeliverable until the provider suppression is cleared",
		zap.String("identifier_type", identifierType),
		zap.String("identifier", identifier),
		zap.String("provider", record.Provider),
		zap.String("reason", record.Reason))
}

// handlePermanentDeliveryFailure is the single response to a permanent,
// recipient-level send failure: give the person their OTP budget back, and
// remember the address so the async path stops claiming a code is on its way.
// Keeping both in one place is what stops the two halves drifting apart.
func (s *verificationService) handlePermanentDeliveryFailure(ctx context.Context, identifierType, identifier string, deliveryErr error) {
	s.refundSendAttempt(ctx, identifierType, identifier)
	s.markUndeliverable(ctx, identifierType, identifier, deliveryErr)
}

// refundSendAttempt gives back the rate-limit budget that checkSendRateLimits
// consumed for an attempt that failed for a permanent, recipient-level reason
// (the provider refuses this address: suppressed, invalid, blocked).
//
// The budget is charged before the send so that a flood of requests cannot
// outrun the limiter. That is right for transient failures, which a retry can
// fix, and wrong for a permanent one: the OTP can never arrive, so charging the
// person turns one undeliverable address into a 24-hour lockout with no way out
// except a different address. Releasing the counters lets them immediately try
// another address, and lets us try again if the address is ever cleared.
func (s *verificationService) refundSendAttempt(ctx context.Context, identifierType, identifier string) {
	refundCtx, cancel := withTimeout(ctx, redisOperationTimeout)
	defer cancel()

	keys := newOTPAttemptKeys(identifierType, identifier)
	for _, key := range []string{keys.minute, keys.hourly, keys.daily} {
		count, err := s.redisClient.IncrBy(refundCtx, key, -1)
		if err != nil {
			s.logger.Warn("Failed to refund OTP send attempt",
				zap.Error(err), zap.String("key", key), zap.String("identifier", identifier))
			continue
		}
		// A counter that reaches zero must be dropped rather than left at 0:
		// the next check counts it, and a negative value would be invisible to
		// the limiter forever.
		if count <= 0 {
			_ = s.redisClient.Del(refundCtx, key)
		}
	}
	_ = s.redisClient.Del(refundCtx, keys.cooldown)

	s.logger.Info("Refunded OTP attempt budget after permanent delivery failure",
		zap.String("identifier_type", identifierType), zap.String("identifier", identifier))
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

			// A permanent recipient rejection cannot be fixed by sending again,
			// and every extra attempt against a refusing provider works against
			// our sending reputation. Stop, and give the person their budget back.
			if entities.IsPermanentEmailDeliveryError(lastErr) {
				break
			}

			if attempt < sendRetryCount {
				time.Sleep(sendRetryBackoff * time.Duration(attempt))
			}
		}

		if lastErr != nil {
			if entities.IsPermanentEmailDeliveryError(lastErr) {
				s.handlePermanentDeliveryFailure(context.Background(), req.identifierType, req.identifier, lastErr)
			}

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
