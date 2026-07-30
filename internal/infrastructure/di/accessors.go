package di

import (
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/account"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/captcha"
	"github.com/rail-service/rail_service/pkg/ratelimit"
)

// GetOnboardingService returns the onboarding service
func (c *Container) GetOnboardingService() *onboarding.Service {
	return c.OnboardingService
}

// GetPasscodeService returns the passcode service
func (c *Container) GetPasscodeService() *passcode.Service {
	return c.PasscodeService
}

// GetSessionService returns the session service
func (c *Container) GetSessionService() *session.Service {
	return c.SessionService
}

// GetTwoFAService returns the 2FA service
func (c *Container) GetTwoFAService() *twofa.Service {
	return c.TwoFAService
}

// GetSocialAuthService returns the social auth service
func (c *Container) GetSocialAuthService() *socialauth.Service {
	return c.SocialAuthService
}

// GetWebAuthnService returns the WebAuthn service
func (c *Container) GetWebAuthnService() *webauthn.Service {
	return c.WebAuthnService
}

// GetAPIKeyService returns the API key service
func (c *Container) GetAPIKeyService() *apikey.Service {
	return c.APIKeyService
}

// GetWalletService returns the wallet service
func (c *Container) GetWalletService() *wallet.Service {
	return c.WalletService
}

// GetFundingService returns the funding service
func (c *Container) GetFundingService() *funding.Service {
	return c.FundingService
}

// GetWithdrawalService returns the withdrawal service
func (c *Container) GetWithdrawalService() *services.WithdrawalService {
	return c.WithdrawalService
}

// GetInvestingService returns the investing service
func (c *Container) GetInvestingService() *investing.Service {
	return c.InvestingService
}

// GetBalanceService returns the Balance service
func (c *Container) GetBalanceService() *services.BalanceService {
	return c.BalanceService
}

// GetLedgerService returns the Ledger service
func (c *Container) GetLedgerService() *ledger.Service {
	return c.LedgerService
}

// GetVerificationService returns the verification service
func (c *Container) GetVerificationService() services.VerificationService {
	return c.VerificationService
}

// GetOnboardingJobService returns the onboarding job service
func (c *Container) GetOnboardingJobService() *services.OnboardingJobService {
	return c.OnboardingJobService
}

// GetAllocationService returns the allocation service
func (c *Container) GetAllocationService() *allocation.Service {
	return c.AllocationService
}

// GetAutoInvestService returns the auto-invest service
func (c *Container) GetAutoInvestService() *autoinvest.Service {
	return c.AutoInvestService
}

// GetLimitsService returns the limits service
func (c *Container) GetLimitsService() *limits.Service {
	return c.LimitsService
}

// GetLimitsHandler returns a new limits handler
func (c *Container) GetLimitsHandler() *handlers.LimitsHandler {
	if c.LimitsService == nil {
		return nil
	}
	return handlers.NewLimitsHandler(c.LimitsService, c.Logger)
}

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

// GetOnboardingFraudService returns the onboarding fraud detection service
func (c *Container) GetOnboardingFraudService() *security.OnboardingFraudService {
	return c.OnboardingFraudService
}

// GetSecurityFeaturesRepo returns the security features repository
func (c *Container) GetSecurityFeaturesRepo() *repositories.SecurityFeaturesRepository {
	return c.SecurityFeaturesRepo
}

// GetRiskScoringService returns the transaction risk scoring service
func (c *Container) GetRiskScoringService() *security.RiskScoringService {
	return c.RiskScoringService
}

// GetAddressWhitelistService returns the address whitelist service
func (c *Container) GetAddressWhitelistService() *security.AddressWhitelistService {
	return c.AddressWhitelistService
}

// GetSessionAnomalyService returns the session anomaly detection service
func (c *Container) GetSessionAnomalyService() *security.SessionAnomalyService {
	return c.SessionAnomalyService
}

// GetWithdrawalLimitsService returns the tiered withdrawal limits service
func (c *Container) GetWithdrawalLimitsService() *security.WithdrawalLimitsService {
	return c.WithdrawalLimitsService
}

// GetAdaptiveMFAService returns the adaptive MFA service
func (c *Container) GetAdaptiveMFAService() *security.AdaptiveMFAService {
	return c.AdaptiveMFAService
}

// GetDeviceSecurityService returns the device security service
func (c *Container) GetDeviceSecurityService() *security.DeviceSecurityService {
	return c.DeviceSecurityService
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

// initializeReconciliationService initializes the reconciliation service and scheduler
