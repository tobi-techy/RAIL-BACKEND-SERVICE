package di

import (
	"github.com/rail-service/rail_service/internal/domain/services/account"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/captcha"
	"github.com/rail-service/rail_service/pkg/ratelimit"
)

// GetLoginProtectionService returns the login protection service
func (c *Container) GetLoginProtectionService() *security.LoginProtectionService {
	return c.LoginProtectionService
}

// GetDeviceTrackingService returns the device tracking service
func (c *Container) GetDeviceTrackingService() *security.DeviceTrackingService {
	return c.DeviceTrackingService
}

// GetWithdrawalSecurityService returns the withdrawal security service
func (c *Container) GetWithdrawalSecurityService() *security.WithdrawalSecurityService {
	return c.WithdrawalSecurityService
}

// GetIPWhitelistService returns the IP whitelist service
func (c *Container) GetIPWhitelistService() *security.IPWhitelistService {
	return c.IPWhitelistService
}

// GetPasswordPolicyService returns the password policy service
func (c *Container) GetPasswordPolicyService() *security.PasswordPolicyService {
	return c.PasswordPolicyService
}

// GetSecurityEventLogger returns the security event logger
func (c *Container) GetSecurityEventLogger() *security.SecurityEventLogger {
	return c.SecurityEventLogger
}

// GetPasswordService returns the enhanced password service
func (c *Container) GetPasswordService() *security.PasswordService {
	return c.PasswordService
}

// GetMFAService returns the unified MFA service
func (c *Container) GetMFAService() *security.MFAService {
	return c.MFAService
}

// GetGeoSecurityService returns the geo security service
func (c *Container) GetGeoSecurityService() *security.GeoSecurityService {
	return c.GeoSecurityService
}

// GetFraudDetectionService returns the fraud detection service
func (c *Container) GetFraudDetectionService() *security.FraudDetectionService {
	return c.FraudDetectionService
}

// GetIncidentResponseService returns the incident response service
func (c *Container) GetIncidentResponseService() *security.IncidentResponseService {
	return c.IncidentResponseService
}

// GetTokenBlacklist returns the token blacklist service
func (c *Container) GetTokenBlacklist() *auth.TokenBlacklist {
	return c.TokenBlacklist
}

// GetJWTService returns the enhanced JWT service
func (c *Container) GetJWTService() *auth.JWTService {
	return c.JWTService
}

// GetTieredRateLimiter returns the tiered rate limiter
func (c *Container) GetTieredRateLimiter() *ratelimit.TieredLimiter {
	return c.TieredRateLimiter
}

// GetAccountDeletionService returns the account deletion service
func (c *Container) GetAccountDeletionService() *account.DeletionService {
	return c.AccountDeletionService
}

// GetRateLimitConfig returns the rate limit configuration
func (c *Container) GetRateLimitConfig() *config.RateLimitConfig {
	return &c.Config.RateLimit
}

// GetLoginAttemptTracker returns the login attempt tracker
func (c *Container) GetLoginAttemptTracker() *ratelimit.LoginAttemptTracker {
	return c.LoginAttemptTracker
}

// GetCaptchaVerifier returns the CAPTCHA verifier (may be nil if not configured)
func (c *Container) GetCaptchaVerifier() *captcha.Verifier {
	return c.CaptchaVerifier
}

// GetWithdrawalSecurityStore returns the withdrawal security store
func (c *Container) GetWithdrawalSecurityStore() *repositories.WithdrawalSecurityStore {
	return c.WithdrawalSecurityStore
}

// GetDepositSecurityStore returns the deposit security store
func (c *Container) GetDepositSecurityStore() *repositories.DepositSecurityStore {
	return c.DepositSecurityStore
}
