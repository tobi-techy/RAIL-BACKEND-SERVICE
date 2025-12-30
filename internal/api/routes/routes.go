package routes

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/pkg/tracing"
)

type SessionValidatorAdapter struct {
	svc *session.Service
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

	// Global middleware - order matters for security
	router.Use(tracing.HTTPMiddleware()) // Tracing should be early in the chain
	router.Use(middleware.RequestID())
	router.Use(middleware.MetricsMiddleware())
	router.Use(middleware.RequestSizeLimit())
	router.Use(middleware.InputValidation())
	router.Use(middleware.Logger(container.Logger))
	router.Use(middleware.Recovery(container.Logger))
	router.Use(middleware.CORS(container.Config.Server.AllowedOrigins))
	router.Use(middleware.RateLimit(container.Config.Server.RateLimitPerMin))
	router.Use(middleware.SecurityHeaders())
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

	// Swagger documentation (development only)
	if container.Config.Environment != "production" {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
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

	authHandlers := handlers.NewAuthHandlers(
		container.DB,
		container.Config,
		container.ZapLog,
		*container.UserRepo,
		container.GetVerificationService(),
		*container.GetOnboardingJobService(),
		container.GetOnboardingService(),
		container.EmailService,
		container.KYCProvider,
		container.GetSessionService(),
		container.GetTwoFAService(),
		container.RedisClient,
	)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.Config,
		container.ZapLog,
	)

	// Initialize social auth handlers
	socialAuthHandlers := handlers.NewSocialAuthHandlers(
		container.GetSocialAuthService(),
		container.GetWebAuthnService(),
		*container.UserRepo,
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

	// Create session validator adapter
	sessionValidator := NewSessionValidatorAdapter(container.GetSessionService())

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Authentication routes (no auth required, no CSRF - API clients don't need CSRF protection)
		auth := v1.Group("/auth")
		{
			auth.POST("/register", authHandlers.Register)
			auth.POST("/refresh", authHandlers.RefreshToken)
			auth.POST("/logout", authHandlers.Logout)
			auth.POST("/verify-email", authHandlers.VerifyEmail)
			auth.POST("/resend-code", authHandlers.ResendCode)

			// Sensitive auth endpoints with stricter rate limiting (5 requests/minute)
			// Protects against brute force attacks on credentials and verification codes
			authRateLimited := auth.Group("/")
			authRateLimited.Use(middleware.AuthRateLimit(5))
			{
				authRateLimited.POST("/login", authHandlers.Login)
				authRateLimited.POST("/verify-code", authHandlers.VerifyCode)
				authRateLimited.POST("/forgot-password", authHandlers.ForgotPassword)
				authRateLimited.POST("/reset-password", authHandlers.ResetPassword)
			}

			// Social auth routes (no auth required)
			authRateLimited.POST("/social/url", socialAuthHandlers.GetSocialAuthURL)
			authRateLimited.POST("/social/login", socialAuthHandlers.SocialLogin)

			// WebAuthn login (no auth required)
			authRateLimited.POST("/webauthn/login/begin", socialAuthHandlers.BeginWebAuthnLogin)
		}

		// Onboarding routes - OpenAPI spec compliant (no CSRF for public start endpoint)
		onboarding := v1.Group("/onboarding")
		{
			onboarding.POST("/start", authHandlers.StartOnboarding)

			authenticatedOnboarding := onboarding.Group("/")
			authenticatedOnboarding.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator))
			{
				authenticatedOnboarding.GET("/status", authHandlers.GetOnboardingStatus)
				authenticatedOnboarding.GET("/progress", authHandlers.GetOnboardingProgress)
				authenticatedOnboarding.POST("/complete", authHandlers.CompleteOnboarding)
				authenticatedOnboarding.POST("/kyc/submit", authHandlers.SubmitKYC)
			}
		}

		// KYC provider webhooks (no auth required for external callbacks)
		kyc := v1.Group("/kyc")
		{
			kyc.POST("/callback/:provider_ref", authHandlers.ProcessKYCCallback)
		}

		// Protected routes (auth required)
		protected := v1.Group("/")
		protected.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator))
		protected.Use(middleware.CSRFProtection(csrfStore))
		{
			// User management
			users := protected.Group("/users")
			{
				users.GET("/me", authHandlers.GetProfile)
				users.PUT("/me", authHandlers.UpdateProfile)
				users.POST("/me/change-password", authHandlers.ChangePassword)
				users.DELETE("/me", authHandlers.DeleteAccount)
				users.POST("/me/enable-2fa", authHandlers.Enable2FA)
				users.POST("/me/disable-2fa", authHandlers.Disable2FA)
			}

			// KYC status utilities (auth required but no KYC gate)
			kycProtected := protected.Group("/kyc")
			{
				kycProtected.GET("/status", authHandlers.GetKYCStatus)
				kycProtected.GET("/verification-url", authHandlers.GetKYCVerificationURL)
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
				security.POST("/passkeys/register", socialAuthHandlers.BeginWebAuthnRegistration)
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
				security.POST("/devices/:id/trust", securityEnhancedHandlers.TrustDevice)
				security.DELETE("/devices/:id", securityEnhancedHandlers.RevokeDevice)

				// IP whitelist management
				security.GET("/ip-whitelist", securityEnhancedHandlers.GetIPWhitelist)
				security.POST("/ip-whitelist", securityEnhancedHandlers.AddIPToWhitelist)
				security.POST("/ip-whitelist/:id/verify", securityEnhancedHandlers.VerifyWhitelistedIP)
				security.DELETE("/ip-whitelist/:id", securityEnhancedHandlers.RemoveIPFromWhitelist)

				// Security events
				security.GET("/events", securityEnhancedHandlers.GetSecurityEvents)
				security.GET("/current-ip", securityEnhancedHandlers.GetCurrentIP)

				// Withdrawal confirmation
				security.POST("/withdrawals/confirm", securityEnhancedHandlers.ConfirmWithdrawal)

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
			}

			// Mobile-optimized API endpoints for better app performance
			mobile := protected.Group("/mobile")
			{
				mobileHandlers := handlers.NewMobileHandlers(
					container.StationService,
					container.GetAllocationService(),
					container.GetInvestingService(),
					*container.UserRepo,
					container.ZapLog,
				)
				mobile.GET("/home", mobileHandlers.GetMobileHome)
				mobile.POST("/batch", mobileHandlers.BatchExecute)
				mobile.POST("/sync", mobileHandlers.Sync)
			}

			// Funding routes (OpenAPI spec compliant)
			funding := protected.Group("/funding")
			{
				funding.POST("/deposit/address", walletFundingHandlers.CreateDepositAddress)
				funding.GET("/confirmations", walletFundingHandlers.GetFundingConfirmations)
				funding.POST("/virtual-account", walletFundingHandlers.CreateVirtualAccount)
				funding.GET("/transactions", walletFundingHandlers.GetTransactionHistory)
			}

			// Balance routes (part of funding but separate for clarity)
			protected.GET("/balances", walletFundingHandlers.GetBalances)

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
				// Investment Stash endpoint - comprehensive investment data
				investmentStashHandlers := container.GetInvestmentStashHandlers()
				if investmentStashHandlers != nil {
					account.GET("/investment-stash", investmentStashHandlers.GetInvestmentStash)
				}
			}

			// Limits routes - deposit/withdrawal limits based on KYC tier
			limits := protected.Group("/limits")
			{
				limitsHandler := container.GetLimitsHandler()
				if limitsHandler != nil {
					limits.GET("", limitsHandler.GetUserLimits())
					limits.POST("/validate/deposit", limitsHandler.ValidateDeposit())
					limits.POST("/validate/withdrawal", limitsHandler.ValidateWithdrawal())
				}
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
							c.JSON(500, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to get baskets"})
							return
						}
						c.JSON(200, gin.H{"baskets": basketList})
					})
					baskets.GET("/:id", func(c *gin.Context) {
						// Get basket by ID
						ctx := c.Request.Context()
						basketID, err := uuid.Parse(c.Param("id"))
						if err != nil {
							c.JSON(400, gin.H{"error": "INVALID_ID", "message": "Invalid basket ID"})
							return
						}
						basket, err := investingService.GetBasket(ctx, basketID)
						if err != nil {
							c.JSON(500, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to get basket"})
							return
						}
						if basket == nil {
							c.JSON(404, gin.H{"error": "NOT_FOUND", "message": "Basket not found"})
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
							c.JSON(400, gin.H{"error": "INVALID_ID", "message": "Invalid basket ID"})
							return
						}
						var req struct {
							Amount string `json:"amount" binding:"required"`
						}
						if err := c.ShouldBindJSON(&req); err != nil {
							c.JSON(400, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
							return
						}
						amount, err := decimal.NewFromString(req.Amount)
						if err != nil {
							c.JSON(400, gin.H{"error": "INVALID_AMOUNT", "message": "Invalid amount format"})
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
							c.JSON(500, gin.H{"error": "INVESTMENT_FAILED", "message": err.Error()})
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
					portfolio.GET("/allocations", portfolioActivityHandlers.GetAllocations)
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
				aiChatHandlers := handlers.NewAIChatHandlers(container.GetAIOrchestrator(), container.Logger)
				aiGroup := protected.Group("/ai")
				{
					aiGroup.POST("/chat", aiChatHandlers.Chat)
					aiGroup.GET("/wrapped", aiChatHandlers.GetWrapped)
					aiGroup.GET("/quick-insight", aiChatHandlers.QuickInsight)
					aiGroup.GET("/suggestions", aiChatHandlers.GetSuggestedQuestions)
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

			// Alpaca Assets - Tradable stocks and ETFs
			assets := protected.Group("/assets")
			{
				assets.GET("/", integrationHandlers.GetAssets)
				assets.GET("/:symbol_or_id", integrationHandlers.GetAsset)
			}

			// Allocation routes - 70/30 Smart Allocation Mode
			allocation := protected.Group("/user/:id/allocation")
			{
				allocation.POST("/enable", allocationHandlers.EnableAllocationMode)
				allocation.POST("/pause", allocationHandlers.PauseAllocationMode)
				allocation.POST("/resume", allocationHandlers.ResumeAllocationMode)
				allocation.GET("/status", allocationHandlers.GetAllocationStatus)
				allocation.GET("/balances", allocationHandlers.GetAllocationBalances)
			}
		}

		// Admin bootstrap route (enforces super admin token after initial creation)
		// v1.POST("/admin/users", authHandlers.CreateAdmin)

		// Admin routes (admin auth required)
		admin := v1.Group("/admin")
		admin.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator))
		admin.Use(middleware.AdminAuth(container.DB, container.Logger))
		admin.Use(middleware.CSRFProtection(csrfStore))
		{
			// Wallet admin routes
			admin.POST("/wallet/create", walletFundingHandlers.CreateWalletsForUser)
			admin.POST("/wallet/retry-provisioning", walletFundingHandlers.RetryWalletProvisioning)
			admin.GET("/wallet/health", walletFundingHandlers.HealthCheck)

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
		webhooks := v1.Group("/webhooks")
		{
			webhooks.POST("/chain-deposit", walletFundingHandlers.ChainDepositWebhook)
			webhooks.POST("/brokerage-fill", walletFundingHandlers.BrokerageFillWebhook)
			
			// Bridge webhooks for fiat deposits and transfers
			if bridgeWebhookHandler := container.GetBridgeWebhookHandler(); bridgeWebhookHandler != nil {
				webhooks.POST("/bridge", bridgeWebhookHandler.HandleWebhook)
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
			)
		}

		// Register round-up routes
		RegisterRoundupRoutes(
			v1,
			container.GetRoundupHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
		)

		// Register copy trading routes
		if container.GetCopyTradingHandlers() != nil {
			copyTradingHandlers := container.GetCopyTradingHandlers()
			authMiddleware := middleware.Authentication(container.Config, container.Logger, sessionValidator)
			SetupCopyTradingRoutes(v1, copyTradingHandlers, authMiddleware)
		}

		// Register card routes
		RegisterCardRoutes(
			v1,
			container.GetCardHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
		)
	}

	// ZeroG and dedicated AI-CFO HTTP routes have been removed.

	return router
}
