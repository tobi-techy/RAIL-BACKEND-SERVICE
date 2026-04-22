package routes

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	kychandlers "github.com/rail-service/rail_service/internal/api/handlers/kyc"
	securityHandlersV2 "github.com/rail-service/rail_service/internal/api/handlers/security"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	kycservice "github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	alpacaadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	diditadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	sumsubadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/sumsub"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/alerting"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"github.com/rail-service/rail_service/pkg/tracing"
)

type SessionValidatorAdapter struct {
	svc *session.Service
}

type WithdrawalWalletProviderAdapter struct {
	getWalletByUserAndChain func(context.Context, uuid.UUID, entities.WalletChain) (*entities.ManagedWallet, error)
}

func (a *WithdrawalWalletProviderAdapter) GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	if a == nil || a.getWalletByUserAndChain == nil {
		return nil, fmt.Errorf("wallet provider not configured")
	}

	normalized := entities.WalletChain(strings.ToUpper(strings.TrimSpace(chain)))
	wallet, err := a.getWalletByUserAndChain(ctx, userID, normalized)
	if err == nil {
		return wallet, nil
	}

	return nil, err
}

func NewSessionValidatorAdapter(svc *session.Service) *SessionValidatorAdapter {
	return &SessionValidatorAdapter{svc: svc}
}

func (a *SessionValidatorAdapter) ValidateSession(ctx context.Context, token string) (*middleware.SessionInfo, error) {
	sess, err := a.svc.ValidateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	return &middleware.SessionInfo{
		ID:     sess.ID,
		UserID: sess.UserID,
	}, nil
}

// SetupRoutes configures all application routes
func SetupRoutes(container *di.Container) *gin.Engine {
	router := gin.New()

	// Configure trusted proxies for secure IP detection in rate limiting
	// This prevents IP spoofing via X-Forwarded-For headers
	// In production, set this to your actual proxy/load balancer IPs
	trustedProxies := container.Config.Server.TrustedProxies
	if len(trustedProxies) == 0 {
		// Default: trust only localhost (for local development with nginx/proxy)
		trustedProxies = []string{"127.0.0.1", "::1"}
	}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		// Log warning but continue - ClientIP will fall back to RemoteAddr
		container.Logger.Warn("Failed to set trusted proxies: %v", err)
	}

	// Lightweight ping — no middleware, no DB, for uptime monitoring
	router.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	// Global middleware - order matters for security
	router.Use(tracing.HTTPMiddleware()) // Tracing should be early in the chain
	router.Use(middleware.RequestID())
	router.Use(middleware.MetricsMiddleware())
	router.Use(middleware.RequestSizeLimit())
	router.Use(middleware.InputValidation())
	router.Use(middleware.Logger(container.Logger))
	router.Use(middleware.Recovery(container.Logger))
	router.Use(middleware.ErrorAlerter(alerting.NewTelegramAlerter(
		container.Config.TelegramAlerts.BotToken,
		container.Config.TelegramAlerts.ChatID,
	)))
	router.Use(middleware.CORS(container.Config.Server.AllowedOrigins, container.Config.Environment))
	router.Use(createRateLimitMiddleware(container))
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.DeviceFingerprintExtractor())
	router.Use(middleware.APIVersionMiddleware(container.Config.Server.SupportedVersions))
	router.Use(middleware.PaginationMiddleware())

	// CSRF protection
	csrfStore := middleware.NewCSRFStore()
	router.Use(middleware.CSRFToken(csrfStore))

	// Initialize handlers with services from DI container
	coreHandlers := handlers.NewCoreHandlers(container.DB, container.Logger)
	allocationHandlers := handlers.NewAllocationHandlers(
		container.GetAllocationService(),
		container.Logger,
	)

	// Health checks (no auth required)
	router.GET("/health", coreHandlers.Health)
	router.GET("/ready", coreHandlers.Ready)
	router.GET("/live", coreHandlers.Live)
	router.GET("/version", coreHandlers.Version)
	router.GET("/metrics", coreHandlers.Metrics)

	// Internal ops endpoints — protected by dedicated INTERNAL_API_KEY (not JWT secret)
	// Rate limited: 5 requests/minute to prevent abuse
	internalHandlers := handlers.NewInternalHandlers(container.DB, container.Config.Security.InternalAPIKey, container.ZapLog)
	internal := router.Group("/internal")
	internal.Use(middleware.RateLimit(5))
	{
		internal.GET("/users/lookup", internalHandlers.LookupUser)
		internal.DELETE("/users/:id", internalHandlers.DeleteUser)
	}

	// Internal knowledge ingestion — no JWT, uses INTERNAL_API_KEY
	if container.GetKnowledgeService() != nil {
		knowledgeHandlers := handlers.NewKnowledgeHandlers(container.GetKnowledgeService(), container.ZapLog)
		internal.POST("/knowledge/ingest", func(c *gin.Context) {
			key := c.GetHeader("Authorization")
			if len(key) > 7 && key[:7] == "Bearer " {
				key = key[7:]
			}
			if container.Config.Security.InternalAPIKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(container.Config.Security.InternalAPIKey)) != 1 {
				c.JSON(401, gin.H{"error": "unauthorized"})
				return
			}
			knowledgeHandlers.Ingest(c)
		})
	}

	// Manual deposit credit — internal API key auth, no user JWT needed
	if container.FundingService != nil {
		internal.POST("/deposit/credit", func(c *gin.Context) {
			// Auth check using internal API key
			key := c.GetHeader("Authorization")
			if len(key) > 7 && key[:7] == "Bearer " {
				key = key[7:]
			}
			if container.Config.Security.InternalAPIKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(container.Config.Security.InternalAPIKey)) != 1 {
				c.JSON(401, gin.H{"error": "unauthorized"})
				return
			}
			var req struct {
				Address string `json:"address" binding:"required"`
				Amount  string `json:"amount" binding:"required"`
				TxHash  string `json:"tx_hash" binding:"required"`
				Chain   string `json:"chain"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}

			// Security: cap single deposit credits at $10,000 USD to limit blast radius of compromised keys
			amt, err := decimal.NewFromString(req.Amount)
			if err != nil || amt.LessThanOrEqual(decimal.Zero) {
				c.JSON(400, gin.H{"error": "invalid amount"})
				return
			}
			maxDeposit := decimal.NewFromInt(10000)
			if amt.GreaterThan(maxDeposit) {
				c.JSON(400, gin.H{"error": "amount exceeds maximum allowed deposit of 10000 USD"})
				return
			}

			// Audit trail for internal deposit credits
			container.Logger.Info("internal deposit credit requested",
				zap.String("client_ip", c.ClientIP()),
				zap.String("amount", req.Amount),
				zap.String("address", req.Address),
				zap.String("tx_hash", req.TxHash),
			)
			chain := strings.ToUpper(req.Chain)
			if chain == "" {
				chain = "BASE"
			}
			deposit := &entities.ChainDepositWebhook{
				Chain:     entities.Chain(chain),
				Address:   req.Address,
				Token:     entities.StablecoinUSDC,
				Amount:    req.Amount,
				TxHash:    req.TxHash,
				BlockTime: time.Now(),
			}
			if err := container.FundingService.ProcessChainDeposit(c.Request.Context(), deposit); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"status": "credited"})
		})
	}

	// Apple App Site Association — required for passkey Associated Domains
	router.GET("/.well-known/apple-app-site-association", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("static/.well-known/apple-app-site-association")
	})
	router.GET("/apple-app-site-association", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("static/.well-known/apple-app-site-association")
	})

	// Swagger documentation (development only, password-protected)
	if container.Config.Environment == "development" {
		swaggerPassword := strings.TrimSpace(os.Getenv("SWAGGER_PASSWORD"))
		if swaggerPassword != "" {
			router.GET("/swagger/*any", func(c *gin.Context) {
				if c.Query("key") != swaggerPassword {
					c.Header("WWW-Authenticate", `Basic realm="Swagger"`)
					c.AbortWithStatus(http.StatusUnauthorized)
					return
				}
				ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
			})
		} else {
			router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
		}
	}
	walletFundingHandlers := handlers.NewWalletFundingHandlers(
		container.GetWalletService(),
		container.GetFundingService(),
		container.GetWithdrawalService(),
		container.GetInvestingService(),
		container.Logger,
	)
	// Configure webhook secret - only skip verification in development when secret is not set
	skipWebhookVerify := container.Config.Environment == "development" && container.Config.Payment.WebhookSecret == ""
	walletFundingHandlers.SetWebhookSecret(container.Config.Payment.WebhookSecret, skipWebhookVerify)

	// Wire user profile provider for withdrawal AlpacaAccountID lookup
	walletFundingHandlers.SetUserProfileProvider(container.UserRepo)

	// Wire ledger service for reconciliation
	walletFundingHandlers.SetLedgerService(container.LedgerService)

	// Wire allocation service for unified balance queries
	if allocationSvc := container.GetAllocationService(); allocationSvc != nil {
		walletFundingHandlers.SetAllocationBalanceProvider(allocationSvc)
	}
	var walletLookup func(context.Context, uuid.UUID, entities.WalletChain) (*entities.ManagedWallet, error)
	if ws := container.GetWalletService(); ws != nil {
		walletLookup = ws.GetWalletByUserAndChain
	}

	withdrawalHandlers := handlers.NewWithdrawalHandlers(
		container.GetWithdrawalService(),
		&WithdrawalWalletProviderAdapter{
			getWalletByUserAndChain: walletLookup,
		},
		container.Logger,
	)

	authHandlers := handlers.NewAuthHandlers(
		container.DB,
		container.Config,
		container.ZapLog,
		*container.UserRepo,
		container.GetVerificationService(),
		container.GetOnboardingService(),
		container.EmailService,
		container.GetSessionService(),
		container.GetTwoFAService(),
		container.GetPasscodeService(),
		container.RedisClient,
		container.GetAccountDeletionService(),
		container.Config.KYC.WebhookSecret,
		container.GetSocialAuthService(),
	)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.GetSessionService(),
		container.Config,
		container.ZapLog,
	)

	// Initialize social auth handlers
	socialAuthHandlers := handlers.NewSocialAuthHandlers(
		container.GetSocialAuthService(),
		container.GetWebAuthnService(),
		container.GetSessionService(),
		container.GetPasscodeService(),
		*container.UserRepo,
		container.RedisClient,
		container.Config,
		container.ZapLog,
	)

	// Initialize integration handlers (Alpaca only - Due replaced by Bridge)
	integrationHandlers := handlers.NewIntegrationHandlers(
		container.AlpacaClient,
		nil, // Due service removed - Bridge handles virtual accounts
		"",  // Due webhook secret removed
		services.NewNotificationService(container.ZapLog),
		container.Logger,
	)

	// Initialize Bridge KYC handlers for optimized KYC flow
	bridgeKYCHandlers := handlers.NewBridgeKYCHandlers(
		container.BridgeClient,
		*container.UserRepo,
		container.ZapLog,
	)
	var sumsubClient kycservice.SumsubAdapter
	if strings.EqualFold(strings.TrimSpace(container.Config.KYC.Provider), "sumsub") &&
		container.Config.KYC.APIKey != "" &&
		container.Config.KYC.APISecret != "" {
		sumsubClient = sumsubadapter.NewClient(sumsubadapter.Config{
			BaseURL:       container.Config.KYC.BaseURL,
			AppToken:      container.Config.KYC.APIKey,
			SecretKey:     container.Config.KYC.APISecret,
			WebhookSecret: container.Config.KYC.WebhookSecret,
			LevelName:     container.Config.KYC.LevelName,
			UserAgent:     container.Config.KYC.UserAgent,
			Timeout:       30 * time.Second,
		}, container.ZapLog)
	}

	var diditClient *diditadapter.Client
	if container.Config.KYC.DiditAPIKey != "" && container.Config.KYC.DiditWorkflowID != "" {
		diditClient = diditadapter.NewClient(diditadapter.Config{
			APIKey:        container.Config.KYC.DiditAPIKey,
			WebhookSecret: container.Config.KYC.DiditWebhookSecret,
			WorkflowID:    container.Config.KYC.DiditWorkflowID,
		}, container.ZapLog)
	}
	kycUserRepoAdapter := repositories.NewKYCUserRepositoryAdapter(container.UserRepo)
	var kycService *kycservice.Service
	if diditClient != nil {
		kycService = kycservice.NewService(
			kycUserRepoAdapter,
			container.KYCSubmissionRepo,
			container.BridgeAdapter,
			alpacaadapter.NewAdapter(container.AlpacaClient, container.Logger),
			sumsubClient,
			container.SumsubWebhookEventRepo,
			container.KYCSyncJobRepo,
			container.Config.KYC.LevelName,
			container.Config.Security.EncryptionKey,
			container.ZapLog,
			diditClient,
		)
	} else {
		kycService = kycservice.NewService(
			kycUserRepoAdapter,
			container.KYCSubmissionRepo,
			container.BridgeAdapter,
			alpacaadapter.NewAdapter(container.AlpacaClient, container.Logger),
			sumsubClient,
			container.SumsubWebhookEventRepo,
			container.KYCSyncJobRepo,
			container.Config.KYC.LevelName,
			container.Config.Security.EncryptionKey,
			container.ZapLog,
		)
	}
	kycHTTPHandlers := kychandlers.NewHandler(kycService, container.Logger)
	if container.NotificationService != nil {
		kycService.SetNotifier(container.NotificationService)
	}
	if container.ComplianceService != nil {
		kycService.SetAMLScreener(container.ComplianceService)
	}
	kycEligibilityMiddleware := middleware.NewKYCMiddleware(container.UserRepo, container.Logger)

	// Create session validator adapter
	sessionValidator := NewSessionValidatorAdapter(container.GetSessionService())

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Authentication routes (no auth required)
		auth := v1.Group("/auth")
		auth.Use(middleware.AuthCSRFProtection())
		{
			auth.POST("/register", middleware.AuthRateLimit(5), authHandlers.Register)
			auth.POST("/verify", middleware.AuthRateLimit(5), authHandlers.Verify)
			auth.POST("/refresh", middleware.AuthRateLimit(10), authHandlers.RefreshToken)
			auth.POST("/resend-code", authHandlers.ResendCode)

			// Sensitive auth endpoints with stricter rate limiting
			authRateLimited := auth.Group("/")
			authRateLimited.Use(middleware.AuthRateLimit(5))
			if container.LoginAttemptTracker != nil {
				authRateLimited.Use(middleware.LoginRateLimiting(container.LoginAttemptTracker, container.GetCaptchaVerifier(), container.Logger))
			}
			if lp := container.GetLoginProtectionService(); lp != nil {
				authRateLimited.Use(middleware.LoginProtection(lp, container.ZapLog))
			}
			if anomalySvc := container.GetSessionAnomalyService(); anomalySvc != nil {
				authRateLimited.Use(middleware.SessionAnomalyDetection(anomalySvc, container.ZapLog))
			}
			{
				authRateLimited.POST("/login", authHandlers.Login)
				authRateLimited.POST("/passcode-login", authHandlers.PasscodeLogin)
				authRateLimited.POST("/forgot-password", authHandlers.ForgotPassword)
				authRateLimited.POST("/verify-reset-code", authHandlers.VerifyResetCode)
				authRateLimited.POST("/reset-password", authHandlers.ResetPassword)
			}

			// Social auth routes
			authRateLimited.POST("/social/url", socialAuthHandlers.GetSocialAuthURL)
			authRateLimited.POST("/social/login", socialAuthHandlers.SocialLogin)
			authRateLimited.POST("/webauthn/login/begin", socialAuthHandlers.BeginWebAuthnLogin)
			authRateLimited.POST("/webauthn/login/finish", socialAuthHandlers.FinishWebAuthnLogin)
		}

		// Onboarding routes
		onboarding := v1.Group("/onboarding")
		{
			authenticatedOnboarding := onboarding.Group("/")
			authenticatedOnboarding.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
			{
				authenticatedOnboarding.POST("/basic-complete", authHandlers.BasicCompleteOnboarding)
				// Fraud detection: correlate device fingerprint across accounts at onboarding completion.
				// Catches fraud rings using purchased KYC identities from the same device.
				if fraudSvc := container.GetOnboardingFraudService(); fraudSvc != nil {
					authenticatedOnboarding.POST("/complete", middleware.OnboardingFraudMiddleware(fraudSvc, container.ZapLog), authHandlers.CompleteOnboarding)
				} else {
					authenticatedOnboarding.POST("/complete", authHandlers.CompleteOnboarding)
				}
			}
		}

		// KYC provider webhooks (no auth required for external callbacks)
		// NOTE: Signature verification is handled inside each handler:
		// - ProcessKYCCallback: verifies via h.verifyKYCCallbackSignature() when secret is configured
		// - HandleSumsubWebhook: verifies via kycService.VerifySumsubWebhookSignature()
		// - HandleDiditWebhook: verifies via kycService.VerifyDiditWebhookSignature()
		kyc := v1.Group("/kyc")
		{
			kyc.POST("/callback/:provider_ref", authHandlers.ProcessKYCCallback)
			if sumsubClient != nil {
				kyc.POST("/sumsub/webhook", kycHTTPHandlers.HandleSumsubWebhook)
			}
			if diditClient != nil {
				kyc.POST("/didit/webhook", kycHTTPHandlers.HandleDiditWebhook)
				// Didit transaction monitoring webhook
				if container.ComplianceService != nil {
					kyc.POST("/didit/transaction-webhook", func(c *gin.Context) {
						body, err := io.ReadAll(c.Request.Body)
						if err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
							return
						}
						sig := c.GetHeader("X-Signature-V2")
						ts := c.GetHeader("X-Timestamp")
						if err := diditClient.VerifyWebhookSignature(body, sig, ts); err != nil {
							container.ZapLog.Warn("Invalid Didit transaction webhook signature", zap.Error(err))
							c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
							return
						}
						var payload diditadapter.TransactionWebhookPayload
						if err := json.Unmarshal(body, &payload); err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
							return
						}
						if err := container.ComplianceService.HandleTransactionWebhook(c.Request.Context(), &payload); err != nil {
							container.ZapLog.Error("Failed to handle transaction webhook", zap.Error(err))
							c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
							return
						}
						c.JSON(http.StatusOK, gin.H{"status": "ok"})
					})
				}
			}
		}

		// Protected routes (auth required)
		protected := v1.Group("/")
		protected.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
		protected.Use(middleware.CSRFProtection(csrfStore))
		{
			// Logout (requires valid session)
			protected.POST("/auth/logout", authHandlers.Logout)

			// User management
			users := protected.Group("/users")
			{
				users.GET("/me", authHandlers.GetProfile)
				users.PUT("/me", authHandlers.UpdateProfile)
				users.POST("/me/change-password", authHandlers.ChangePassword)
				users.DELETE("/me", middleware.AuthRateLimit(3), authHandlers.DeleteAccount)
				users.POST("/me/enable-2fa", authHandlers.Enable2FA)
				users.POST("/me/disable-2fa", authHandlers.Disable2FA)
			}

			// KYC status utilities (auth required but no KYC gate)
			kycProtected := protected.Group("/kyc")
			{
				kycProtected.POST("/sumsub/session", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.CreateSumsubSession)
				kycProtected.GET("/sumsub/token", middleware.AuthRateLimit(10), kycHTTPHandlers.RefreshSumsubToken)
				kycProtected.POST("/didit/session", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.CreateDiditSession)
				kycProtected.POST("/submit", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.SubmitKYC)
				kycProtected.GET("/status", kycHTTPHandlers.GetKYCStatus)
				// Bridge KYC - optimized for sub-2-minute verification
				kycProtected.GET("/bridge/link", bridgeKYCHandlers.GetBridgeKYCLink)
				kycProtected.GET("/bridge/status", bridgeKYCHandlers.GetBridgeKYCStatus)
			}

			// Security routes for passcode management
			security := protected.Group("/security")
			{
				security.GET("/passcode", securityHandlers.GetPasscodeStatus)
				security.POST("/passcode", securityHandlers.CreatePasscode)
				security.PUT("/passcode", securityHandlers.UpdatePasscode)
				security.POST("/passcode/verify", securityHandlers.VerifyPasscode)
				security.DELETE("/passcode", securityHandlers.RemovePasscode)

				// Social account management
				security.GET("/social-accounts", socialAuthHandlers.GetLinkedAccounts)
				security.POST("/social-accounts/link", socialAuthHandlers.LinkSocialAccount)
				security.DELETE("/social-accounts/:provider", socialAuthHandlers.UnlinkSocialAccount)

				// WebAuthn/Passkey management
				security.GET("/passkeys", socialAuthHandlers.GetWebAuthnCredentials)
				security.POST("/passkeys/register", middleware.AuthRateLimit(5), socialAuthHandlers.BeginWebAuthnRegistration)
				security.POST("/passkeys/register/finish", middleware.AuthRateLimit(5), socialAuthHandlers.FinishWebAuthnRegistration)
				security.DELETE("/passkeys/:id", socialAuthHandlers.DeleteWebAuthnCredential)

				// Device management
				securityEnhancedHandlers := handlers.NewSecurityEnhancedHandlers(
					container.GetDeviceTrackingService(),
					container.GetIPWhitelistService(),
					container.GetWithdrawalSecurityService(),
					container.GetSecurityEventLogger(),
					container.ZapLog,
				)
				security.GET("/devices", securityEnhancedHandlers.GetDevices)

				// IP whitelist management
				security.GET("/ip-whitelist", securityEnhancedHandlers.GetIPWhitelist)
				security.POST("/ip-whitelist/:id/verify", securityEnhancedHandlers.VerifyWhitelistedIP)

				// Security events
				security.GET("/events", securityEnhancedHandlers.GetSecurityEvents)
				security.GET("/current-ip", securityEnhancedHandlers.GetCurrentIP)

				// MFA management
				mfaHandlers := handlers.NewMFAHandlers(
					container.GetMFAService(),
					container.GetGeoSecurityService(),
					container.GetIncidentResponseService(),
					container.ZapLog,
				)
				security.GET("/mfa", mfaHandlers.GetMFASettings)
				security.POST("/mfa/sms", mfaHandlers.SetupSMSMFA)
				security.POST("/mfa/send-code", mfaHandlers.SendMFACode)
				security.POST("/mfa/verify", mfaHandlers.VerifyMFACode)
				security.GET("/geo-info", mfaHandlers.GetGeoInfo)

				// Sensitive operations require a short-lived passcode session token.
				securitySensitive := security.Group("/")
				securitySensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
				{
					securitySensitive.POST("/devices/:id/trust", securityEnhancedHandlers.TrustDevice)
					securitySensitive.DELETE("/devices/:id", securityEnhancedHandlers.RevokeDevice)
					securitySensitive.POST("/ip-whitelist", securityEnhancedHandlers.AddIPToWhitelist)
					securitySensitive.DELETE("/ip-whitelist/:id", securityEnhancedHandlers.RemoveIPFromWhitelist)
					securitySensitive.POST("/withdrawals/confirm", securityEnhancedHandlers.ConfirmWithdrawal)
				}

				// Security features v2: address whitelist + adaptive MFA
				securityFeaturesHandler := securityHandlersV2.NewSecurityFeaturesHandler(
					container.GetAddressWhitelistService(),
					container.GetAdaptiveMFAService(),
					container.ZapLog,
				)
				RegisterSecurityFeatureRoutes(security, securityFeaturesHandler)
			}

			// Mobile-optimized API endpoints for better app performance
			mobile := protected.Group("/mobile")
			{
				mobileHandlers := handlers.NewMobileHandlers(
					container.StationService,
					container.GetAllocationService(),
					container.GetInvestingService(),
					*container.UserRepo,
					container.CardRepo,
					container.ZapLog,
				)
				mobile.GET("/home", mobileHandlers.GetMobileHome)
				mobile.POST("/batch", mobileHandlers.BatchExecute)
				mobile.POST("/sync", mobileHandlers.Sync)
			}

			// Funding routes (legacy - kept for backward compat, prefer /deposits)
			funding := protected.Group("/funding")
			funding.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			{
				funding.GET("/transactions", walletFundingHandlers.GetTransactionHistory)
				// Pre-KYC: TOS link needed during onboarding, read-only Paj lookups
				funding.GET("/tos-link", walletFundingHandlers.GetBridgeTOSLink)
				if container.PajHandlers != nil {
					paj := funding.Group("/paj")
					paj.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					paj.GET("/rates", container.PajHandlers.GetRates)
					paj.GET("/banks", container.PajHandlers.GetBanks)
					paj.GET("/orders", container.PajHandlers.GetOrders)
					paj.GET("/orders/:id/status", container.PajHandlers.GetOrderStatus)
				}

				// KYC-gated funding operations
				fundingGated := funding.Group("/")
				fundingGated.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
				{
					fundingGated.POST("/deposit/address", walletFundingHandlers.CreateDepositAddress)
					fundingGated.POST("/virtual-account", walletFundingHandlers.CreateVirtualAccount)
					fundingGated.GET("/virtual-accounts", walletFundingHandlers.GetVirtualAccounts)

					if instantFundingHandlers := container.GetInstantFundingHandlers(); instantFundingHandlers != nil {
						fundingGated.POST("/instant", instantFundingHandlers.RequestInstantFunding)
						fundingGated.GET("/instant/status", instantFundingHandlers.GetInstantFundingStatus)
					}

					if container.ChainRailsHandlers != nil {
						chainrails := fundingGated.Group("/chainrails")
						chainrails.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
						chainrails.POST("/session",
							middleware.AuthRateLimit(10),
							container.ChainRailsHandlers.CreateSession)
					}

					if container.PajHandlers != nil {
						pajGated := fundingGated.Group("/paj")
						pajGated.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
						pajGated.POST("/initiate", middleware.AuthRateLimit(5), container.PajHandlers.Initiate)
						pajGated.POST("/verify", middleware.AuthRateLimit(10), container.PajHandlers.Verify)
						pajGated.POST("/banks/resolve", container.PajHandlers.ResolveBankAccount)
						pajGated.POST("/banks/add", container.PajHandlers.AddBankAccount)
						pajGated.GET("/banks/saved", container.PajHandlers.GetBankAccounts)
						pajGated.POST("/onramp", middleware.AuthRateLimit(10), container.PajHandlers.CreateOnramp)
						pajGated.POST("/offramp", middleware.AuthRateLimit(10), container.PajHandlers.CreateOfframp)
					}
				}
			}

			// Unified Balance route
			protected.GET("/balances", walletFundingHandlers.GetUnifiedBalances)

			// Unified Deposit routes
			deposits := protected.Group("/deposits")
			deposits.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			{
				// Read-only: no KYC gate — all authenticated users can view their deposits
				deposits.GET("", walletFundingHandlers.ListDeposits)
				deposits.GET("/:id", walletFundingHandlers.GetDeposit)
			}
			// Write operations require Bridge KYC
			depositsGated := deposits.Group("/")
			depositsGated.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
			// Fraud detection on deposit creation: catches suspicious first deposits from fraud ring accounts.
			if fraudSvc := container.GetOnboardingFraudService(); fraudSvc != nil {
				depositsGated.Use(middleware.DepositFraudMiddleware(fraudSvc, container.ZapLog))
			}
			{
				depositsGated.POST("", walletFundingHandlers.CreateDeposit)
			}

			// Unified Withdrawal routes with security middleware
			withdrawals := protected.Group("/withdrawals")
			withdrawals.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			withdrawals.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
			// Apply withdrawal security: rate limits (3/day) and daily max ($10k new, $100k established)
			if withdrawalSecurityStore := container.GetWithdrawalSecurityStore(); withdrawalSecurityStore != nil {
				withdrawals.Use(middleware.WithdrawalSecurityMiddleware(
					withdrawalSecurityStore,
					middleware.DefaultWithdrawalSecurityConfig(),
				))
			}
			// Enforce passcode-verified session for new withdrawal initiation requests.
			withdrawalsSensitive := withdrawals.Group("/")
			withdrawalsSensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
			{
				// Keep POST /withdrawals for backward compatibility and treat it as crypto withdrawal.
				withdrawalsSensitive.POST("", withdrawalHandlers.InitiateCryptoWithdrawal)
				withdrawalsSensitive.POST("/crypto", withdrawalHandlers.InitiateCryptoWithdrawal)
				withdrawalsSensitive.POST("/fiat", withdrawalHandlers.InitiateFiatWithdrawal)
				withdrawals.GET("/fees", withdrawalHandlers.GetWithdrawalFees)
				withdrawals.GET("", withdrawalHandlers.GetUserWithdrawals)
				withdrawals.GET("/:withdrawalId", withdrawalHandlers.GetWithdrawal)
				withdrawals.DELETE("/:withdrawalId", withdrawalHandlers.CancelWithdrawal)
			}

			// Account routes - Station (home screen) endpoint
			account := protected.Group("/account")
			{
				stationHandlers := container.GetStationHandlers()
				if stationHandlers != nil {
					// Station endpoint - returns home screen data per PRD
					// "Total balance, Spend balance, Invest balance, System status"
					account.GET("/station", stationHandlers.GetStation)
				}

				// Spending Stash endpoint - comprehensive spending data
				spendingStashHandlers := container.GetSpendingStashHandlers()
				if spendingStashHandlers != nil {
					account.GET("/spending-stash", spendingStashHandlers.GetSpendingStash)
				}

				// Investment Stash endpoint - comprehensive investment data
				investmentStashHandlers := container.GetInvestmentStashHandlers()
				if investmentStashHandlers != nil {
					account.GET("/investment-stash", investmentStashHandlers.GetInvestmentStash)
					account.GET("/investment-stash/positions", investmentStashHandlers.GetInvestmentPositions)
					account.GET("/investment-stash/distribution", investmentStashHandlers.GetInvestmentDistribution)
					account.GET("/investment-stash/transactions", investmentStashHandlers.GetInvestmentTransactions)
					account.GET("/investment-stash/performance", investmentStashHandlers.GetInvestmentPerformance)
				}

				// Yield estimate endpoint
				if container.YieldService != nil {
					yieldHandlers := handlers.NewYieldHandlers(container.YieldService, container.AllocationService, container.ZapLog)
					account.GET("/yield/estimate", yieldHandlers.GetDailyYieldEstimate)
				}
			}

			// Limits routes - deposit/withdrawal limits based on KYC tier
			limits := protected.Group("/limits")

			// Gameplay routes - streaks, XP, challenges, achievements, subscription
			SetupGameplayRoutes(protected, container)

			{
				limitsHandler := container.GetLimitsHandler()
				if limitsHandler != nil {
					limits.GET("", limitsHandler.GetUserLimits())
					limits.POST("/validate/deposit", limitsHandler.ValidateDeposit())
					limits.POST("/validate/withdrawal", limitsHandler.ValidateWithdrawal())
				}
			}

			// P2P Transfer routes - Cash App style money transfers
			// Rate limited: 20 send/lookup per minute, 10 cancel per minute
			p2pHandlers := container.GetP2PHandlers()
			if p2pHandlers != nil {
				p2p := protected.Group("/p2p")
				p2p.Use(middleware.AuthRateLimit(20))
				p2p.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
				{
					p2p.POST("/lookup", middleware.AuthRateLimit(10), p2pHandlers.Lookup)
					p2p.POST("/send", p2pHandlers.Send)
					p2p.GET("/transfers", p2pHandlers.GetTransfers)
					p2p.GET("/recent", p2pHandlers.GetRecentRecipients)
					p2p.DELETE("/transfers/:id", middleware.AuthRateLimit(10), p2pHandlers.Cancel)
					p2p.POST("/claim/:token", p2pHandlers.ClaimByToken)
					p2p.POST("/railtag", p2pHandlers.SetRailTag)
					p2p.POST("/railtag/check", p2pHandlers.CheckRailTag)

					// Tap-to-pay secure handshake
					p2p.POST("/tap/intent", p2pHandlers.TapIntent)
					tapSensitive := p2p.Group("/tap")
					tapSensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
					tapSensitive.POST("/confirm", p2pHandlers.TapConfirm)
				}
			}

			// Public P2P claim routes (no auth required — for web claim page)
			// Rate limited: 10 per minute by IP to prevent enumeration attacks
			if p2pHandlers != nil {
				publicP2P := router.Group("/api/v1/p2p")
				publicP2P.Use(middleware.AuthRateLimit(10))
				{
					publicP2P.GET("/claim/:token", p2pHandlers.GetClaimInfo)
					publicP2P.POST("/claim/:token/bank", p2pHandlers.ClaimToBank)
				}
			}

			// Household expense tracking
			if container.P2PService != nil {
				householdHandler := handlers.NewHouseholdHandler(sqlx.NewDb(container.DB, "postgres"), container.P2PService, container.ZapLog)
				household := protected.Group("/household/groups")
				{
					household.POST("", householdHandler.CreateGroup)
					household.POST("/:id/receipts", householdHandler.ShareReceipt)
					household.GET("/:id/summary", householdHandler.GetSummary)
				}
			}

			// Notification routes - push tokens and in-app notifications
			notificationHandlers := handlers.NewNotificationHandlers(
				container.DeviceTokenRepo,
				container.NotificationRepo,
				container.ZapLog,
			)
			devices := protected.Group("/devices")
			{
				devices.POST("/token", notificationHandlers.RegisterDeviceToken)
				devices.DELETE("/token", notificationHandlers.UnregisterDeviceToken)
			}
			notifications := protected.Group("/notifications")
			{
				notifications.GET("", notificationHandlers.GetNotifications)
				notifications.GET("/unread-count", notificationHandlers.GetUnreadCount)
				notifications.POST("/:id/read", notificationHandlers.MarkAsRead)
				notifications.POST("/read-all", notificationHandlers.MarkAllAsRead)
			}

			// Investment routes
			basketExecutor := container.InitializeBasketExecutor()
			investingService := container.GetInvestingService()
			if basketExecutor != nil && investingService != nil {
				// Curated baskets endpoints
				baskets := protected.Group("/baskets")
				{
					baskets.GET("", func(c *gin.Context) {
						// Get curated baskets
						ctx := c.Request.Context()
						basketList, err := investingService.ListBaskets(ctx)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get baskets")
							return
						}
						c.JSON(200, gin.H{"baskets": basketList})
					})
					baskets.GET("/:id", func(c *gin.Context) {
						// Get basket by ID
						ctx := c.Request.Context()
						basketID, err := uuid.Parse(c.Param("id"))
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid basket ID")
							return
						}
						basket, err := investingService.GetBasket(ctx, basketID)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get basket")
							return
						}
						if basket == nil {
							common.SendNotFound(c, common.ErrCodeBasketNotFound, "Basket not found")
							return
						}
						c.JSON(200, basket)
					})
					baskets.POST("/:id/invest", func(c *gin.Context) {
						// Invest in basket
						ctx := c.Request.Context()
						userID, _ := uuid.Parse(c.GetString("user_id"))
						basketID, err := uuid.Parse(c.Param("id"))
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid basket ID")
							return
						}
						var req struct {
							Amount string `json:"amount" binding:"required"`
						}
						if err := c.ShouldBindJSON(&req); err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
							return
						}
						amount, err := decimal.NewFromString(req.Amount)
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Invalid amount format")
							return
						}
						// Create order request
						orderReq := &entities.OrderCreateRequest{
							BasketID: basketID,
							Side:     entities.OrderSideBuy,
							Amount:   amount.String(),
						}
						order, err := investingService.CreateOrder(ctx, userID, orderReq)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeOperationFailed, err.Error())
							return
						}
						c.JSON(201, gin.H{"order": order})
					})
				}
			}

			// Wallet routes (OpenAPI spec compliant)
			wallet := protected.Group("/wallet")
			{
				wallet.GET("/addresses", walletFundingHandlers.GetWalletAddresses)
				wallet.GET("/status", walletFundingHandlers.GetWalletStatus)
			}

			// Enhanced wallet endpoints
			wallets := protected.Group("/wallets")
			{
				wallets.POST("/initiate", walletFundingHandlers.InitiateWalletCreation)
				wallets.POST("/provision", walletFundingHandlers.ProvisionWallets)
				wallets.GET("/:chain/address", walletFundingHandlers.GetWalletByChain)
			}

			// Portfolio endpoints (STACK MVP spec compliant)
			portfolio := protected.Group("/portfolio")
			{
				portfolio.GET("/overview", walletFundingHandlers.GetPortfolio)

				// AI Financial Manager - Portfolio endpoints
				if container.GetPortfolioDataProvider() != nil {
					portfolioActivityHandlers := handlers.NewPortfolioActivityHandlers(
						container.GetPortfolioDataProvider(),
						container.GetActivityDataProvider(),
						container.GetStreakRepository(),
						container.GetContributionsRepository(),
						container.Logger,
					)
					portfolio.GET("/weekly-stats", portfolioActivityHandlers.GetWeeklyStats)
					portfolio.GET("/top-movers", portfolioActivityHandlers.GetTopMovers)
					portfolio.GET("/performance", portfolioActivityHandlers.GetPerformance)
				}
			}

			// Activity endpoints (AI Financial Manager)
			if container.GetActivityDataProvider() != nil {
				activity := protected.Group("/activity")
				{
					portfolioActivityHandlers := handlers.NewPortfolioActivityHandlers(
						container.GetPortfolioDataProvider(),
						container.GetActivityDataProvider(),
						container.GetStreakRepository(),
						container.GetContributionsRepository(),
						container.Logger,
					)
					activity.GET("/contributions", portfolioActivityHandlers.GetContributions)
					activity.GET("/streak", portfolioActivityHandlers.GetStreak)
					activity.GET("/timeline", portfolioActivityHandlers.GetTimeline)
				}
			}

			// AI Chat endpoints (AI Financial Manager)
			if container.GetAIOrchestrator() != nil {
				aiChatHandlers := handlers.NewAIChatHandlers(container.GetAIOrchestrator(), container.GetConversationService(), container.Logger)
				aiGroup := protected.Group("/ai")
				{
					aiGroup.POST("/chat", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), aiChatHandlers.Chat)
					aiGroup.POST("/chat/stream", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), aiChatHandlers.ChatStream)
					aiGroup.GET("/wrapped", middleware.AuthRateLimit(10), aiChatHandlers.GetWrapped)
					aiGroup.GET("/quick-insight", middleware.AuthRateLimit(20), aiChatHandlers.QuickInsight)
					aiGroup.GET("/suggestions", aiChatHandlers.GetSuggestedQuestions)

					// Image analysis (receipt scanning)
					if container.Config.AI.OpenAI.APIKey != "" {
						imageHandler := handlers.NewImageAnalysisHandler(
							container.Config.AI.OpenAI.APIKey,
							container.GetAIOrchestrator(),
							container.ReceiptRepo,
							container.ZapLog,
						)
						imageHandler.SetBudgetRepo(container.BudgetRepo)
						imageHandler.SetSpendingRepo(container.LedgerSpendingRepo)
						aiGroup.POST("/chat/image", middleware.AuthRateLimit(10), imageHandler.AnalyzeImage)
						aiGroup.POST("/chat/images", middleware.AuthRateLimit(3), imageHandler.BatchAnalyzeImages)
						aiGroup.GET("/receipts", imageHandler.GetReceipts)
						aiGroup.GET("/receipts/gallery", imageHandler.GetReceiptGallery)
						aiGroup.PUT("/receipts/:id", imageHandler.UpdateReceipt)
						aiGroup.DELETE("/receipts/:id", imageHandler.DeleteReceipt)

						// Receipt split with friends
						if container.P2PService != nil {
							splitHandler := handlers.NewReceiptSplitHandler(container.ReceiptRepo, container.P2PService, container.ZapLog)
							aiGroup.POST("/receipts/:id/split", splitHandler.SplitReceipt)
						}
					}

					// Premium AI endpoints (pro-gated)
					if container.SubscriptionService != nil {
						premiumHandlers := handlers.NewPremiumAIHandlers(
							container.GetAIOrchestrator(),
							container.SubscriptionService,
							container.ZapLog,
						)
						aiGroup.GET("/report/weekly", middleware.AuthRateLimit(5), premiumHandlers.WeeklyReport)
						aiGroup.POST("/simulate", middleware.AuthRateLimit(10), premiumHandlers.Simulate)
						aiGroup.GET("/tax-summary", middleware.AuthRateLimit(5), premiumHandlers.TaxSummary)
						aiGroup.POST("/challenge/generate", middleware.AuthRateLimit(10), premiumHandlers.GenerateChallenge)
						aiGroup.GET("/goals/progress", middleware.AuthRateLimit(10), premiumHandlers.GoalProgress)
					}

					// Voice session (WebSocket)
					if container.Config.AI.OpenAI.APIKey != "" {
						voiceHandler := handlers.NewVoiceHandler(
							container.Config.AI.OpenAI.APIKey,
							container.Config.AI.OpenAI.RealtimeModel,
							container.GetAIOrchestrator(),
							container.GetUsageService(),
							container.Config.Server.AllowedOrigins,
							container.ZapLog,
						)
						aiGroup.GET("/voice/session", voiceHandler.HandleSession)
					}
				}

				// Conversation endpoints
				if container.GetConversationService() != nil {
					convHandlers := handlers.NewConversationHandlers(container.GetAIOrchestrator(), container.GetConversationService(), container.ZapLog)
					convGroup := protected.Group("/ai/conversations")
					{
						convGroup.POST("", convHandlers.CreateConversation)
						convGroup.GET("", convHandlers.ListConversations)
						convGroup.GET("/:id", convHandlers.GetConversation)
						convGroup.DELETE("/:id", convHandlers.DeleteConversation)
						convGroup.POST("/:id/chat", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), convHandlers.ChatInConversation)
						convGroup.POST("/:id/confirm", convHandlers.ConfirmAction)
						convGroup.POST("/:id/cancel", convHandlers.CancelAction)
					}
				}

				// Usage tracking endpoint
				if container.GetUsageService() != nil {
					usageHandlers := handlers.NewUsageHandlers(container.GetUsageService(), container.ZapLog)
					aiGroup.GET("/usage", usageHandlers.GetUsage)
				}
			}

			// News endpoints (AI Financial Manager)
			if container.GetNewsService() != nil {
				newsHandlers := handlers.NewNewsHandlers(container.GetNewsService(), container.Logger)
				news := protected.Group("/news")
				{
					news.GET("/feed", newsHandlers.GetFeed)
					news.GET("/weekly", newsHandlers.GetWeeklyNews)
					news.POST("/read", newsHandlers.MarkAsRead)
					news.GET("/unread-count", newsHandlers.GetUnreadCount)
					news.POST("/refresh", newsHandlers.RefreshNews)
				}
			}

			// Alpaca Assets - Tradable stocks and ETFs (cached 5min — asset list rarely changes)
			assets := protected.Group("/assets")
			{
				assets.GET("/", middleware.PublicCache(300), integrationHandlers.GetAssets)
				assets.GET("/:symbol_or_id", middleware.PublicCache(300), integrationHandlers.GetAsset)
			}

			// Allocation routes - 70/30 Smart Allocation Mode (ON/OFF)
			allocation := protected.Group("/allocation")
			allocation.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
			{
				allocation.POST("/enable", allocationHandlers.EnableAllocationMode)
				allocation.POST("/disable", allocationHandlers.DisableAllocationMode)
				allocation.GET("/balances", allocationHandlers.GetAllocationBalances)
			}
		}

		// Admin bootstrap route (enforces super admin token after initial creation)
		// v1.POST("/admin/users", adminHandlers.CreateAdmin)
		// v1.POST("/admin/promote", adminHandlers.PromoteUser)

		// Admin routes (admin auth required)
		admin := v1.Group("/admin")
		admin.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
		admin.Use(middleware.AdminAuth(container.DB, container.Logger))
		admin.Use(middleware.CSRFProtection(csrfStore))
		{
			// User lookup
			admin.GET("/users/lookup", handlers.AdminLookupUser(container.DB))

			// Wallet admin routes
			admin.POST("/wallet/create", walletFundingHandlers.CreateWalletsForUser)
			admin.POST("/wallet/retry-provisioning", walletFundingHandlers.RetryWalletProvisioning)
			admin.GET("/wallet/health", walletFundingHandlers.HealthCheck)
			admin.POST("/reconcile/:user_id", walletFundingHandlers.ReconcileUserBalance)

			// Yield distribution — manually trigger for a period (format: YYYY-MM-DD)
			if container.YieldDistributionWorker != nil {
				admin.POST("/yield/distribute", handlers.TriggerYieldDistribution(container.YieldDistributionWorker, container.ZapLog))
			}

			// Stash reconciliation — manually trigger a check of ledger vs Reflect balance
			if container.StashReconciliation != nil {
				admin.POST("/stash/reconcile", handlers.TriggerStashReconciliation(container.StashReconciliation, container.ZapLog))
			}

			// KYC admin routes
			admin.POST("/kyc/resync-bridge", kycHTTPHandlers.ResyncBridge)
			admin.POST("/kyc/repair-bridge-govid", kycHTTPHandlers.RepairBridgeGovID)

			// Knowledge base admin routes
			if container.GetKnowledgeService() != nil {
				knowledgeHandlers := handlers.NewKnowledgeHandlers(container.GetKnowledgeService(), container.ZapLog)
				admin.POST("/knowledge/ingest", knowledgeHandlers.Ingest)
			}

			// Security admin routes
			adminMFAHandlers := handlers.NewMFAHandlers(
				container.GetMFAService(),
				container.GetGeoSecurityService(),
				container.GetIncidentResponseService(),
				container.ZapLog,
			)
			adminSecurity := admin.Group("/security")
			{
				// Security dashboard
				adminSecurity.GET("/dashboard", adminMFAHandlers.GetSecurityDashboard)

				// Incident management
				adminSecurity.GET("/incidents", adminMFAHandlers.GetOpenIncidents)
				adminSecurity.GET("/incidents/:id", adminMFAHandlers.GetIncident)
				adminSecurity.PUT("/incidents/:id/status", adminMFAHandlers.UpdateIncidentStatus)
				adminSecurity.POST("/incidents/:id/playbook", adminMFAHandlers.ExecutePlaybook)

				// Geo-blocking management
				adminSecurity.GET("/blocked-countries", adminMFAHandlers.GetBlockedCountries)
				adminSecurity.POST("/blocked-countries", adminMFAHandlers.BlockCountry)
				adminSecurity.DELETE("/blocked-countries/:country_code", adminMFAHandlers.UnblockCountry)
			}
		}

		// Webhooks (external systems) - OpenAPI spec compliant
		// Apply webhook security middleware (rate limiting, IP whitelisting, replay protection)
		webhookConfig := middleware.DefaultWebhookSecurityConfig()
		webhookConfig.Environment = container.Config.Environment
		webhookConfig.Secrets = map[string]string{
			"bridge": container.Config.Bridge.WebhookSecret,
			"alpaca": container.Config.Alpaca.WebhookSecret,
		}

		webhooks := v1.Group("/webhooks")
		if redisNative := container.RedisClient.Client(); redisNative != nil {
			webhooks.Use(middleware.WebhookSecurityWithRedisV8(
				redisNative,
				webhookConfig,
				container.ZapLog,
			))
		}
		// Hardened per-provider signature + timestamp verification
		webhookSigSecrets := container.Config.Security.WebhookSignatureSecrets
		if webhookSigSecrets.Bridge != "" || webhookSigSecrets.Alpaca != "" || webhookSigSecrets.Due != "" {
			webhooks.Use(middleware.HardenedWebhookVerification(
				middleware.WebhookProviderConfig{
					BridgeSecret: webhookSigSecrets.Bridge,
					AlpacaSecret: webhookSigSecrets.Alpaca,
					DueSecret:    webhookSigSecrets.Due,
				},
				container.ZapLog,
			))
		}
		{
			webhooks.POST("/chain-deposit", walletFundingHandlers.ChainDepositWebhook)
			webhooks.POST("/brokerage-fill", walletFundingHandlers.BrokerageFillWebhook)

			// Unified funding webhook - routes based on source header/payload
			// POST /webhooks/funding - handles Bridge and Alpaca webhooks
			if unifiedWebhookHandler := container.GetUnifiedFundingWebhookHandler(); unifiedWebhookHandler != nil {
				webhooks.POST("/funding", unifiedWebhookHandler.HandleFundingWebhook)
			}

			// Bridge webhooks for fiat deposits and transfers
			if bridgeWebhookHandler := container.GetBridgeWebhookHandler(); bridgeWebhookHandler != nil {
				webhooks.POST("/bridge", bridgeWebhookHandler.HandleWebhook)
				// Synchronous real-time card authorization webhook (Bridge calls this during transactions)
				webhooks.POST("/bridge/card-auth", bridgeWebhookHandler.HandleRealTimeAuth)
			}

			// ChainRails webhooks for cross-chain deposits
			if container.ChainRailsHandlers != nil {
				chainrailsWebhooks := webhooks.Group("/chainrails")
				chainrailsWebhooks.Use(middleware.RateLimit(100)) // 100 req/min per IP
				chainrailsWebhooks.POST("", container.ChainRailsHandlers.HandleWebhook)
			}

			// Paj Cash webhooks (per-order, no signature verification — verified by polling)
			if container.PajHandlers != nil {
				pajWebhooks := webhooks.Group("/paj")
				pajWebhooks.Use(middleware.RateLimit(100))
				pajWebhooks.POST("", container.PajHandlers.HandleWebhook)
			}
		}

		// Register Alpaca investment routes
		if container.GetInvestmentHandlers() != nil {
			RegisterAlpacaRoutes(
				v1,
				container.GetInvestmentHandlers(),
				container.GetAlpacaWebhookHandlers(),
				container.Config,
				container.Logger,
				sessionValidator,
				container.UserRepo,
				container.TokenBlacklist,
			)
		}

		// Register advanced features routes (analytics, market, scheduled investments, rebalancing)
		if container.GetAnalyticsHandlers() != nil {
			RegisterAdvancedFeaturesRoutes(
				v1,
				container.GetAnalyticsHandlers(),
				container.GetMarketHandlers(),
				container.GetScheduledInvestmentHandlers(),
				container.GetRebalancingHandlers(),
				container.Config,
				container.Logger,
				sessionValidator,
				container.TokenBlacklist,
			)
		}

		// Register round-up routes
		RegisterRoundupRoutes(
			v1,
			container.GetRoundupHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
			container.TokenBlacklist,
		)

		// Register copy trading routes
		if container.GetCopyTradingHandlers() != nil {
			copyTradingHandlers := container.GetCopyTradingHandlers()
			authMiddleware := middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist)
			SetupCopyTradingRoutes(v1, copyTradingHandlers, authMiddleware)
		}

		// Register card routes
		RegisterCardRoutes(
			v1,
			container.GetCardHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
			container.UserRepo,
			container.TokenBlacklist,
		)
	}

	// ZeroG and dedicated AI-CFO HTTP routes have been removed.

	return router
}

func createDistributedRateLimiter(container *di.Container) *ratelimit.DistributedRateLimiter {
	rateLimitConfig := container.GetRateLimitConfig()

	limiter := container.GetTieredRateLimiter()

	return ratelimit.NewDistributedRateLimiter(limiter, *rateLimitConfig, container.Logger.Zap())
}

func createRateLimitMiddleware(container *di.Container) gin.HandlerFunc {
	distributedRL := createDistributedRateLimiter(container)
	return distributedRL.Middleware()
}
