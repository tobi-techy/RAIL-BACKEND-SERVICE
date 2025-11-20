package routes

import (
	"github.com/stack-service/stack_service/internal/api/handlers"
	"github.com/stack-service/stack_service/internal/api/middleware"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/infrastructure/di"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// SetupRoutes configures all application routes
func SetupRoutes(container *di.Container) *gin.Engine {
	router := gin.New()

	// Global middleware - order matters for security
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

	// Health checks (no auth required)
	healthHandler := handlers.NewHealthHandler(container.DB, container.Logger)
	router.GET("/health", healthHandler.Health)
	router.GET("/ready", healthHandler.Ready)
	router.GET("/live", healthHandler.Live)
	router.GET("/version", handlers.VersionHandler())
	router.GET("/metrics", handlers.Metrics())

	// Swagger documentation (development only)
	if container.Config.Environment != "production" {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// Initialize handlers with services from DI container
	fundingHandlers := handlers.NewFundingHandlers(container.GetFundingService(), nil, container.Logger)
	onboardingHandlers := handlers.NewOnboardingHandlers(container.GetOnboardingService(), container.ZapLog)
	walletHandlers := handlers.NewWalletHandlers(container.GetWalletService(), container.ZapLog)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.Config,
		container.ZapLog,
	)

	// Initialize Due handlers
	notificationService := services.NewNotificationService(container.ZapLog)
	dueHandlers := handlers.NewDueHandler(container.GetDueService(), notificationService, container.Logger)

	// Initialize AI-CFO and ZeroG handlers
	aicfoHandlers := handlers.NewAICfoHandler(container.GetAICfoService(), container.ZapLog)
	zeroGHandlers := handlers.NewZeroGHandler(
	container.GetStorageClient(),
	 container.GetInferenceGateway(),
		container.GetNamespaceManager(),
		container.ZapLog,
	)

	// Initialize Alpaca handlers
	alpacaHandlers := handlers.NewAlpacaHandlers(container.AlpacaClient, container.ZapLog)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Authentication routes (no auth required)
		auth := v1.Group("/auth")
		auth.Use(middleware.CSRFProtection(csrfStore))
		{

			// New signup flow with verification
			authSignupHandlers := handlers.NewAuthSignupHandlers(
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
			auth.POST("/register", authSignupHandlers.Register)
			auth.POST("/verify-code", authSignupHandlers.VerifyCode)
			auth.POST("/resend-code", authSignupHandlers.ResendCode)

			auth.POST("/login", handlers.Login(container.DB, container.Config, container.Logger, container.EmailService))
			auth.POST("/refresh", handlers.RefreshToken(container.DB, container.Config, container.Logger))
			auth.POST("/logout", handlers.Logout(container.DB, container.Config, container.Logger))
			auth.POST("/forgot-password", handlers.ForgotPassword(container.DB, container.Config, container.Logger))
			auth.POST("/reset-password", handlers.ResetPassword(container.DB, container.Config, container.Logger))
			auth.POST("/verify-email", handlers.VerifyEmail(container.DB, container.Config, container.Logger))
		}

		// Onboarding routes - OpenAPI spec compliant
		onboarding := v1.Group("/onboarding")
		onboarding.Use(middleware.CSRFProtection(csrfStore))
		{
			onboarding.POST("/start", onboardingHandlers.StartOnboarding)

			authenticatedOnboarding := onboarding.Group("/")
			authenticatedOnboarding.Use(middleware.Authentication(container.Config, container.Logger))
			{
				authenticatedOnboarding.GET("/status", onboardingHandlers.GetOnboardingStatus)
				authenticatedOnboarding.POST("/kyc/submit", onboardingHandlers.SubmitKYC)
			}
		}

		// KYC provider webhooks (no auth required for external callbacks)
		kyc := v1.Group("/kyc")
		{
			kyc.POST("/callback/:provider_ref", onboardingHandlers.ProcessKYCCallback)
		}

		// Protected routes (auth required)
		protected := v1.Group("/")
		protected.Use(middleware.Authentication(container.Config, container.Logger))
		protected.Use(middleware.CSRFProtection(csrfStore))
		{
			// User management
			users := protected.Group("/users")
			{
				users.GET("/me", handlers.GetProfile(container.DB, container.Config, container.Logger))
				users.PUT("/me", handlers.UpdateProfile(container.DB, container.Config, container.Logger))
				users.POST("/me/change-password", handlers.ChangePassword(container.DB, container.Config, container.Logger))
				users.DELETE("/me", handlers.DeleteAccount(container.DB, container.Config, container.Logger))
				users.POST("/me/enable-2fa", handlers.Enable2FA(container.DB, container.Config, container.Logger))
				users.POST("/me/disable-2fa", handlers.Disable2FA(container.DB, container.Config, container.Logger))
			}

			// KYC status utilities (auth required but no KYC gate)
			kycProtected := protected.Group("/kyc")
			{
				kycProtected.GET("/status", onboardingHandlers.GetKYCStatus)
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
				funding.POST("/deposit/address", fundingHandlers.CreateDepositAddress)
				funding.GET("/confirmations", fundingHandlers.GetFundingConfirmations)
				funding.POST("/virtual-account", fundingHandlers.CreateVirtualAccount)
			}

			// Balance routes (part of funding but separate for clarity)
			protected.GET("/balances", fundingHandlers.GetBalances)

			// Investment routes
			basketExecutor := container.InitializeBasketExecutor()
			investmentHandlers := handlers.NewInvestmentHandlers(
				basketExecutor,
				container.GetBalanceService(),
				container.ZapLog,
			)
			RegisterInvestmentRoutes(protected, investmentHandlers, container.Config, container.Logger)

			// Wallet routes (OpenAPI spec compliant)
			wallet := protected.Group("/wallet")
			{
				wallet.GET("/addresses", walletHandlers.GetWalletAddresses)
				wallet.GET("/status", walletHandlers.GetWalletStatus)
			}

			// Enhanced wallet endpoints
			wallets := protected.Group("/wallets")
			{
				wallets.POST("/initiate", walletHandlers.InitiateWalletCreation)
				wallets.POST("/provision", walletHandlers.ProvisionWallets)
				wallets.GET("/:chain/address", walletHandlers.GetWalletByChain)
			}

			// Investment baskets
			baskets := protected.Group("/baskets")
			{
				baskets.GET("/", handlers.GetBaskets(container.DB, container.Config, container.Logger))
				baskets.POST("/", handlers.CreateBasket(container.DB, container.Config, container.Logger))
				baskets.GET("/:id", handlers.GetBasket(container.DB, container.Config, container.Logger))
				baskets.PUT("/:id", handlers.UpdateBasket(container.DB, container.Config, container.Logger))
				baskets.DELETE("/:id", handlers.DeleteBasket(container.DB, container.Config, container.Logger))
				baskets.POST("/:id/invest", handlers.InvestInBasket(container.DB, container.Config, container.Logger))
				baskets.POST("/:id/withdraw", handlers.WithdrawFromBasket(container.DB, container.Config, container.Logger))
			}

			// Curated baskets (public/featured)
			curated := protected.Group("/curated")
			{
				curated.GET("/baskets", handlers.GetCuratedBaskets(container.DB, container.Config, container.Logger))
				curated.GET("/baskets/:id", handlers.GetCuratedBasket(container.DB, container.Config, container.Logger))
				curated.POST("/baskets/:id/invest", handlers.InvestInCuratedBasket(container.DB, container.Config, container.Logger))
			}

			// Copy trading
			copy := protected.Group("/copy")
			{
				copy.GET("/traders", handlers.GetTopTraders(container.DB, container.Config, container.Logger))
				copy.GET("/traders/:id", handlers.GetTrader(container.DB, container.Config, container.Logger))
				copy.POST("/traders/:id/follow", handlers.FollowTrader(container.DB, container.Config, container.Logger))
				copy.DELETE("/traders/:id/unfollow", handlers.UnfollowTrader(container.DB, container.Config, container.Logger))
				copy.GET("/following", handlers.GetFollowedTraders(container.DB, container.Config, container.Logger))
				copy.GET("/followers", handlers.GetFollowers(container.DB, container.Config, container.Logger))
			}

			// Cards and payments
			cards := protected.Group("/cards")
			{
				cards.Use(middleware.RequireKYC(container.GetOnboardingService(), container.ZapLog))
				cards.GET("/", handlers.GetCards(container.DB, container.Config, container.Logger))
				cards.POST("/", handlers.CreateCard(container.DB, container.Config, container.Logger))
				cards.GET("/:id", handlers.GetCard(container.DB, container.Config, container.Logger))
				cards.PUT("/:id", handlers.UpdateCard(container.DB, container.Config, container.Logger))
				cards.DELETE("/:id", handlers.DeleteCard(container.DB, container.Config, container.Logger))
				cards.POST("/:id/freeze", handlers.FreezeCard(container.DB, container.Config, container.Logger))
				cards.POST("/:id/unfreeze", handlers.UnfreezeCard(container.DB, container.Config, container.Logger))
				cards.GET("/:id/transactions", handlers.GetCardTransactions(container.DB, container.Config, container.Logger))
			}

			// Transactions
			transactions := protected.Group("/transactions")
			{
				transactions.GET("/", handlers.GetTransactions(container.DB, container.Config, container.Logger))
				transactions.GET("/:id", handlers.GetTransaction(container.DB, container.Config, container.Logger))
				transactions.POST("/deposit", handlers.Deposit(container.DB, container.Config, container.Logger))
				transactions.POST("/withdraw",
					middleware.RequireKYC(container.GetOnboardingService(), container.ZapLog),
					handlers.Withdraw(container.DB, container.Config, container.Logger))
				transactions.POST("/transfer", handlers.Transfer(container.DB, container.Config, container.Logger))
				transactions.POST("/swap", handlers.SwapTokens(container.DB, container.Config, container.Logger))
			}

			// Analytics and portfolio
			analytics := protected.Group("/analytics")
			{
				analytics.GET("/portfolio", handlers.GetPortfolioAnalytics(container.DB, container.Config, container.Logger))
				analytics.GET("/performance", handlers.GetPerformanceMetrics(container.DB, container.Config, container.Logger))
				analytics.GET("/allocation", handlers.GetAssetAllocation(container.DB, container.Config, container.Logger))
				analytics.GET("/history", handlers.GetPortfolioHistory(container.DB, container.Config, container.Logger))
			}

			// Portfolio endpoints (STACK MVP spec compliant)
			portfolio := protected.Group("/portfolio")
			{
				stackHandlers := handlers.NewStackHandlers(container.GetFundingService(), container.GetInvestingService(), container.Logger)
				portfolio.GET("/overview", stackHandlers.GetPortfolioOverview)
			}

			// Notifications
			notifications := protected.Group("/notifications")
			{
				notifications.GET("/", handlers.GetNotifications(container.DB, container.Config, container.Logger))
				notifications.PUT("/:id/read", handlers.MarkNotificationRead(container.DB, container.Config, container.Logger))
				notifications.PUT("/read-all", handlers.MarkAllNotificationsRead(container.DB, container.Config, container.Logger))
				notifications.DELETE("/:id", handlers.DeleteNotification(container.DB, container.Config, container.Logger))
			}

			// Alpaca Assets - Tradable stocks and ETFs
			assets := protected.Group("/assets")
			{
				assets.GET("/", alpacaHandlers.GetAssets)                      // List all assets with filtering
				assets.GET("/search", alpacaHandlers.SearchAssets)             // Search assets
				assets.GET("/popular", alpacaHandlers.GetPopularAssets)       // Get popular/trending assets
				assets.GET("/exchange/:exchange", alpacaHandlers.GetAssetsByExchange) // Get assets by exchange
				assets.GET("/:symbol_or_id", alpacaHandlers.GetAsset)         // Get specific asset details
			}
		}

		// Admin bootstrap route (enforces super admin token after initial creation)
		v1.POST("/admin/users", handlers.CreateAdmin(container.DB, container.Config, container.Logger))

		// Admin routes (admin auth required)
		admin := v1.Group("/admin")
		admin.Use(middleware.Authentication(container.Config, container.Logger))
		admin.Use(middleware.AdminAuth(container.DB, container.Logger))
		admin.Use(middleware.CSRFProtection(csrfStore))
		{
			admin.GET("/users", handlers.GetAllUsers(container.DB, container.Config, container.Logger))
			admin.GET("/users/:id", handlers.GetUserByID(container.DB, container.Config, container.Logger))
			admin.PUT("/users/:id/status", handlers.UpdateUserStatus(container.DB, container.Config, container.Logger))
			admin.GET("/transactions", handlers.GetAllTransactions(container.DB, container.Config, container.Logger))
			admin.GET("/analytics/system", handlers.GetSystemAnalytics(container.DB, container.Config, container.Logger))
			admin.POST("/baskets/curated", handlers.CreateCuratedBasket(container.DB, container.Config, container.Logger))
			admin.PUT("/baskets/curated/:id", handlers.UpdateCuratedBasket(container.DB, container.Config, container.Logger))

			// Wallet admin routes
			admin.POST("/wallet/create", walletHandlers.CreateWalletsForUser)
			admin.POST("/wallet/retry-provisioning", walletHandlers.RetryWalletProvisioning)
			admin.GET("/wallet/health", walletHandlers.HealthCheck)

			// Wallet set management
			admin.POST("/wallet-sets", handlers.CreateWalletSet(container.DB, container.Config, container.Logger))
			admin.GET("/wallet-sets", handlers.GetWalletSets(container.DB, container.Config, container.Logger))
			admin.GET("/wallet-sets/:id", handlers.GetWalletSetByID(container.DB, container.Config, container.Logger))

			// Enhanced wallet management
			admin.GET("/wallets", handlers.GetAdminWallets(container.DB, container.Config, container.Logger))
		}

		// Due API routes (protected)
		due := protected.Group("/due")
		{
			// Account management
			due.POST("/account", dueHandlers.CreateDueAccount)
			due.GET("/account", dueHandlers.GetDueAccount)

			// KYC management
			due.GET("/kyc-link", dueHandlers.GetKYCLink)
			due.GET("/kyc-status", dueHandlers.GetKYCStatus)
			due.POST("/initiate-kyc", dueHandlers.InitiateKYC)

			// Terms of Service
			due.POST("/accept-tos", dueHandlers.AcceptTermsOfService)

			// Wallet management
			due.POST("/link-wallet", dueHandlers.LinkWallet)
			due.GET("/wallets", dueHandlers.ListWallets)
			due.GET("/wallets/:wallet_id", dueHandlers.GetWalletByID)

			// Recipients
			due.GET("/recipients", dueHandlers.ListRecipients)
			due.GET("/recipients/:recipient_id", dueHandlers.GetRecipient)

			// Virtual accounts
			due.POST("/virtual-account", dueHandlers.CreateVirtualAccount)
			due.GET("/virtual-accounts", dueHandlers.ListVirtualAccounts)

			// Transfers
			due.POST("/transfer", dueHandlers.CreateTransfer)
			due.GET("/transfers", dueHandlers.ListTransfers)
			due.GET("/transfer/:transfer_id", dueHandlers.GetTransfer)

			// Quotes
			due.POST("/quote", dueHandlers.CreateQuote)

			// Channels (payment methods)
			due.GET("/channels", dueHandlers.GetChannels)

			// Webhook management
			due.POST("/webhooks", dueHandlers.CreateWebhookEndpoint)
			due.GET("/webhooks", dueHandlers.ListWebhookEndpoints)
			due.DELETE("/webhooks/:webhook_id", dueHandlers.DeleteWebhookEndpoint)
		}

		// Webhooks (external systems) - OpenAPI spec compliant
		webhooks := v1.Group("/webhooks")
		{
			webhooks.POST("/chain-deposit", fundingHandlers.ChainDepositWebhook)
			webhooks.POST("/due", dueHandlers.HandleWebhook)
			// webhooks.POST("/brokerage-fill", investingHandlers.BrokerageFillWebhook) // Commented out until service is implemented
			// Legacy webhook handlers (to be deprecated)
			webhooks.POST("/payment", handlers.PaymentWebhook(container.DB, container.Config, container.Logger))
			webhooks.POST("/blockchain", handlers.BlockchainWebhook(container.DB, container.Config, container.Logger))
			webhooks.POST("/cards", handlers.CardWebhook(container.DB, container.Config, container.Logger))
		}
	}

	// Setup ZeroG and AI routes
	SetupZeroGRoutes(router, zeroGHandlers, aicfoHandlers, container.ZapLog)

	return router
}
