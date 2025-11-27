package routes

import (
	"context"

	"github.com/stack-service/stack_service/internal/api/handlers"
	"github.com/stack-service/stack_service/internal/api/middleware"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/domain/services/session"
	"github.com/stack-service/stack_service/internal/infrastructure/di"
	"github.com/stack-service/stack_service/pkg/tracing"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
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
		nil, // FundingWithdrawalService
		container.GetInvestingService(),
		container.Logger,
	)
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
	)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.Config,
		container.ZapLog,
	)

// Initialize integration handlers (Alpaca, Due)
integrationHandlers := handlers.NewIntegrationHandlers(
	container.AlpacaClient,
	container.GetDueService(),
	services.NewNotificationService(container.ZapLog),
	container.Logger,
)

	// Create session validator adapter
	sessionValidator := NewSessionValidatorAdapter(container.GetSessionService())

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Authentication routes (no auth required)
		auth := v1.Group("/auth")
		auth.Use(middleware.CSRFProtection(csrfStore))
		{
			auth.POST("/register", authHandlers.Register)
			auth.POST("/login", authHandlers.Login)
			auth.POST("/refresh", authHandlers.RefreshToken)
			auth.POST("/logout", authHandlers.Logout)
			auth.POST("/forgot-password", authHandlers.ForgotPassword)
			auth.POST("/reset-password", authHandlers.ResetPassword)
			auth.POST("/verify-email", authHandlers.VerifyEmail)
		}

		// Onboarding routes - OpenAPI spec compliant
		onboarding := v1.Group("/onboarding")
		onboarding.Use(middleware.CSRFProtection(csrfStore))
		{
			onboarding.POST("/start", authHandlers.StartOnboarding)

			authenticatedOnboarding := onboarding.Group("/")
			authenticatedOnboarding.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator))
			{
				authenticatedOnboarding.GET("/status", authHandlers.GetOnboardingStatus)
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
			}

			// Security routes for passcode management
			security := protected.Group("/security")
			{
				security.GET("/passcode", securityHandlers.GetPasscodeStatus)
				security.POST("/passcode", securityHandlers.CreatePasscode)
				security.PUT("/passcode", securityHandlers.UpdatePasscode)
				security.POST("/passcode/verify", securityHandlers.VerifyPasscode)
				security.DELETE("/passcode", securityHandlers.RemovePasscode)
			}

			// Funding routes (OpenAPI spec compliant)
			funding := protected.Group("/funding")
			{
				funding.POST("/deposit/address", walletFundingHandlers.CreateDepositAddress)
				funding.GET("/confirmations", walletFundingHandlers.GetFundingConfirmations)
				funding.POST("/virtual-account", walletFundingHandlers.CreateVirtualAccount)
			}

			// Balance routes (part of funding but separate for clarity)
			protected.GET("/balances", walletFundingHandlers.GetBalances)

			// Investment routes
			basketExecutor := container.InitializeBasketExecutor()
			if basketExecutor != nil {
				// TODO: Implement InvestmentHandlers
				// investmentHandlers := handlers.NewInvestmentHandlers(...)
				// RegisterInvestmentRoutes(protected, investmentHandlers, ...)
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
		}

		// Due API routes (protected)
		due := protected.Group("/due")
		{
			// Account management
			due.POST("/account", integrationHandlers.CreateDueAccount)
		}

		// Webhooks (external systems) - OpenAPI spec compliant
		webhooks := v1.Group("/webhooks")
		{
			webhooks.POST("/chain-deposit", walletFundingHandlers.ChainDepositWebhook)
			webhooks.POST("/brokerage-fill", walletFundingHandlers.BrokerageFillWebhook)
		}
	}

	// ZeroG and dedicated AI-CFO HTTP routes have been removed.

	return router
}
