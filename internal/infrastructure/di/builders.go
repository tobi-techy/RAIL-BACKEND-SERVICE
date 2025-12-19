package di

import (
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"go.uber.org/zap"
)

// SecurityServicesBuilder builds security-related services
type SecurityServicesBuilder struct {
	db          *sql.DB
	cfg         *config.Config
	logger      *zap.Logger
	redisClient cache.RedisClient
}

// NewSecurityServicesBuilder creates a new security services builder
func NewSecurityServicesBuilder(db *sql.DB, cfg *config.Config, logger *zap.Logger, redisClient cache.RedisClient) *SecurityServicesBuilder {
	return &SecurityServicesBuilder{
		db:          db,
		cfg:         cfg,
		logger:      logger,
		redisClient: redisClient,
	}
}

// SecurityServices holds all security-related services
type SecurityServices struct {
	SessionService          *session.Service
	TwoFAService            *twofa.Service
	APIKeyService           *apikey.Service
	SocialAuthService       *socialauth.Service
	WebAuthnService         *webauthn.Service
	PasscodeService         *passcode.Service
	LoginProtection         *security.LoginProtectionService
	DeviceTracking          *security.DeviceTrackingService
	WithdrawalSecurity      *security.WithdrawalSecurityService
	IPWhitelist             *security.IPWhitelistService
	PasswordPolicy          *security.PasswordPolicyService
	SecurityEventLogger     *security.SecurityEventLogger
	PasswordService         *security.PasswordService
	MFAService              *security.MFAService
	GeoSecurity             *security.GeoSecurityService
	FraudDetection          *security.FraudDetectionService
	IncidentResponse        *security.IncidentResponseService
	TokenBlacklist          *auth.TokenBlacklist
	JWTService              *auth.JWTService
	TieredRateLimiter       *ratelimit.TieredLimiter
	LoginAttemptTracker     *ratelimit.LoginAttemptTracker
}

// Build builds all security services
func (b *SecurityServicesBuilder) Build(userRepo *repositories.UserRepository) (*SecurityServices, error) {
	services := &SecurityServices{}

	// Core authentication services
	services.SessionService = session.NewService(b.db, b.logger)
	services.TwoFAService = twofa.NewService(b.db, b.logger, b.cfg.Security.EncryptionKey)
	services.APIKeyService = apikey.NewService(b.db, b.logger)

	// Passcode service
	services.PasscodeService = passcode.NewService(userRepo, b.redisClient, b.logger)

	// Social auth
	socialAuthConfig := socialauth.Config{
		Google: socialauth.OAuthConfig{
			ClientID:     b.cfg.SocialAuth.Google.ClientID,
			ClientSecret: b.cfg.SocialAuth.Google.ClientSecret,
			RedirectURI:  b.cfg.SocialAuth.Google.RedirectURI,
		},
		Apple: socialauth.AppleOAuthConfig{
			ClientID:    b.cfg.SocialAuth.Apple.ClientID,
			TeamID:      b.cfg.SocialAuth.Apple.TeamID,
			KeyID:       b.cfg.SocialAuth.Apple.KeyID,
			PrivateKey:  b.cfg.SocialAuth.Apple.PrivateKey,
			RedirectURI: b.cfg.SocialAuth.Apple.RedirectURI,
		},
	}
	services.SocialAuthService = socialauth.NewService(b.db, b.logger, socialAuthConfig)

	// WebAuthn (if configured)
	if b.cfg.WebAuthn.RPID != "" {
		webauthnConfig := webauthn.Config{
			RPDisplayName: b.cfg.WebAuthn.RPDisplayName,
			RPID:          b.cfg.WebAuthn.RPID,
			RPOrigins:     b.cfg.WebAuthn.RPOrigins,
		}
		webauthnSvc, err := webauthn.NewService(b.db, b.logger, webauthnConfig)
		if err != nil {
			b.logger.Warn("Failed to initialize WebAuthn service", zap.Error(err))
		} else {
			services.WebAuthnService = webauthnSvc
		}
	}

	// Security services
	redisNativeClient := b.redisClient.Client()
	services.LoginProtection = security.NewLoginProtectionService(redisNativeClient, b.logger)
	services.DeviceTracking = security.NewDeviceTrackingService(b.db, b.logger)
	services.WithdrawalSecurity = security.NewWithdrawalSecurityService(b.db, redisNativeClient, b.logger)
	services.IPWhitelist = security.NewIPWhitelistService(b.db, redisNativeClient, b.logger)
	services.PasswordPolicy = security.NewPasswordPolicyService(b.cfg.Security.CheckPasswordBreaches)
	services.SecurityEventLogger = security.NewSecurityEventLogger(b.db, b.logger)
	services.PasswordService = security.NewPasswordService(b.db, b.logger, b.cfg.Security.CheckPasswordBreaches)

	// Enhanced security services
	services.MFAService = security.NewMFAService(b.db, redisNativeClient, b.logger, b.cfg.Security.EncryptionKey, nil)
	services.GeoSecurity = security.NewGeoSecurityService(b.db, redisNativeClient, b.logger, "")
	services.FraudDetection = security.NewFraudDetectionService(b.db, redisNativeClient, b.logger)
	services.IncidentResponse = security.NewIncidentResponseService(b.db, redisNativeClient, b.logger, nil, services.SecurityEventLogger)

	// Token management
	if b.cfg.Security.EnableTokenBlacklist {
		services.TokenBlacklist = auth.NewTokenBlacklist(redisNativeClient)
		services.JWTService = auth.NewJWTService(
			b.cfg.JWT.Secret,
			b.cfg.Security.AccessTokenTTL,
			b.cfg.Security.RefreshTokenTTL,
			services.TokenBlacklist,
		)
	}

	// Rate limiting
	tieredConfig := ratelimit.TieredConfig{
		GlobalLimit:  1000,
		GlobalWindow: time.Minute,
		IPLimit:      int64(b.cfg.Server.RateLimitPerMin),
		IPWindow:     time.Minute,
		UserLimit:    200,
		UserWindow:   time.Minute,
		EndpointLimits: map[string]ratelimit.EndpointLimit{
			"POST /api/v1/auth/login":        {Limit: 5, Window: 15 * time.Minute},
			"POST /api/v1/auth/register":     {Limit: 3, Window: time.Hour},
			"POST /api/v1/funding/withdraw":  {Limit: 10, Window: time.Hour},
		},
	}
	services.TieredRateLimiter = ratelimit.NewTieredLimiter(redisNativeClient, tieredConfig, b.logger)
	services.LoginAttemptTracker = ratelimit.NewLoginAttemptTracker(redisNativeClient, b.logger)

	return services, nil
}

// FundingServicesBuilder builds funding-related services
type FundingServicesBuilder struct {
	db          *sql.DB
	sqlxDB      *sqlx.DB
	cfg         *config.Config
	logger      *logger.Logger
	zapLog      *zap.Logger
	alpaca      *alpaca.Client
}

// NewFundingServicesBuilder creates a new funding services builder
func NewFundingServicesBuilder(db *sql.DB, cfg *config.Config, logger *logger.Logger, alpaca *alpaca.Client) *FundingServicesBuilder {
	return &FundingServicesBuilder{
		db:     db,
		sqlxDB: sqlx.NewDb(db, "postgres"),
		cfg:    cfg,
		logger: logger,
		zapLog: logger.Zap(),
		alpaca: alpaca,
	}
}

// FundingServices holds all funding-related services
type FundingServices struct {
	BalanceService      *services.BalanceService
	NotificationService *services.NotificationService
}

// Build builds all funding services
func (b *FundingServicesBuilder) Build(balanceRepo *repositories.BalanceRepository, alpacaFundingAdapter interface{}) (*FundingServices, error) {
	result := &FundingServices{}

	// Notification service
	result.NotificationService = services.NewNotificationService(b.zapLog)

	return result, nil
}

// RepositoryBuilder builds all repositories
type RepositoryBuilder struct {
	db     *sql.DB
	sqlxDB *sqlx.DB
	logger *zap.Logger
	log    *logger.Logger
}

// NewRepositoryBuilder creates a new repository builder
func NewRepositoryBuilder(db *sql.DB, logger *zap.Logger, log *logger.Logger) *RepositoryBuilder {
	return &RepositoryBuilder{
		db:     db,
		sqlxDB: sqlx.NewDb(db, "postgres"),
		logger: logger,
		log:    log,
	}
}

// Repositories holds all repositories
type Repositories struct {
	UserRepo                  *repositories.UserRepository
	OnboardingFlowRepo        *repositories.OnboardingFlowRepository
	KYCSubmissionRepo         *repositories.KYCSubmissionRepository
	WalletRepo                *repositories.WalletRepository
	WalletSetRepo             *repositories.WalletSetRepository
	WalletProvisioningJobRepo *repositories.WalletProvisioningJobRepository
	DepositRepo               *repositories.DepositRepository
	WithdrawalRepo            *repositories.WithdrawalRepository
	ConversionRepo            *repositories.ConversionRepository
	BalanceRepo               *repositories.BalanceRepository
	FundingEventJobRepo       *repositories.FundingEventJobRepository
	LedgerRepo                *repositories.LedgerRepository
	ReconciliationRepo        repositories.ReconciliationRepository
	OnboardingJobRepo         *repositories.OnboardingJobRepository
}

// Build builds all repositories
func (b *RepositoryBuilder) Build() *Repositories {
	return &Repositories{
		UserRepo:                  repositories.NewUserRepository(b.db, b.logger),
		OnboardingFlowRepo:        repositories.NewOnboardingFlowRepository(b.db, b.logger),
		KYCSubmissionRepo:         repositories.NewKYCSubmissionRepository(b.db, b.logger),
		WalletRepo:                repositories.NewWalletRepository(b.db, b.logger),
		WalletSetRepo:             repositories.NewWalletSetRepository(b.db, b.logger),
		WalletProvisioningJobRepo: repositories.NewWalletProvisioningJobRepository(b.db, b.logger),
		DepositRepo:               repositories.NewDepositRepository(b.sqlxDB),
		WithdrawalRepo:            repositories.NewWithdrawalRepository(b.sqlxDB),
		ConversionRepo:            repositories.NewConversionRepository(b.sqlxDB),
		BalanceRepo:               repositories.NewBalanceRepository(b.db, b.logger),
		FundingEventJobRepo:       repositories.NewFundingEventJobRepository(b.db, b.log),
		LedgerRepo:                repositories.NewLedgerRepository(b.sqlxDB),
		ReconciliationRepo:        repositories.NewPostgresReconciliationRepository(b.db),
		OnboardingJobRepo:         repositories.NewOnboardingJobRepository(b.db, b.logger),
	}
}
