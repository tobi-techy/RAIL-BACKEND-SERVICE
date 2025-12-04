package routes

import (
	"database/sql"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// SetupStackRoutes configures STACK MVP routes matching OpenAPI specification
func SetupStackRoutes(db *sql.DB, cfg *config.Config, log *logger.Logger, zapLog *zap.Logger) *gin.Engine {
	router := gin.New()

	// Global middleware
	router.Use(middleware.RequestID())
	router.Use(middleware.Logger(log))
	router.Use(middleware.Recovery(log))
	router.Use(middleware.CORS(cfg.Server.AllowedOrigins))
	router.Use(middleware.RateLimit(cfg.Server.RateLimitPerMin))
	router.Use(middleware.SecurityHeaders())

	// CSRF protection
	csrfStore := middleware.NewCSRFStore()

	// Health check (no auth required)
	coreHandlers := handlers.NewCoreHandlers(db, log)
	router.GET("/health", coreHandlers.Health)
	router.GET("/metrics", coreHandlers.Metrics)

	// Swagger documentation (development only)
	if cfg.Environment != "production" {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// Create session service and adapter
	sessionService := session.NewService(db, zapLog)
	sessionValidator := NewSessionValidatorAdapter(sessionService)

	// Initialize wallet funding handlers
	walletFundingHandlers := handlers.NewWalletFundingHandlers(
		nil, // wallet service
		nil, // funding service
		nil, // FundingWithdrawalService
		nil, // investing service
		log,
	)

	// API v1 routes matching OpenAPI specification
	v1 := router.Group("/v1")
	{
		// === FUNDING ENDPOINTS ===
		funding := v1.Group("/funding")
		funding.Use(middleware.Authentication(cfg, log, sessionValidator))
		funding.Use(middleware.CSRFProtection(csrfStore))
		{
			funding.POST("/deposit/address", walletFundingHandlers.CreateDepositAddress)
			funding.GET("/confirmations", walletFundingHandlers.GetFundingConfirmations)
		}

		// Balance endpoint (separate from funding per OpenAPI)
		balances := v1.Group("/balances")
		balances.Use(middleware.Authentication(cfg, log, sessionValidator))
		{
			balances.GET("", walletFundingHandlers.GetBalances)
		}

		// === INVESTING ENDPOINTS ===
		baskets := v1.Group("/baskets")
		baskets.Use(middleware.Authentication(cfg, log, sessionValidator))
		{
			baskets.GET("", walletFundingHandlers.GetBaskets)
			baskets.GET("/:id", walletFundingHandlers.GetBasket)
		}

		orders := v1.Group("/orders")
		orders.Use(middleware.Authentication(cfg, log, sessionValidator))
		orders.Use(middleware.CSRFProtection(csrfStore))
		{
			orders.POST("", walletFundingHandlers.CreateOrder)
			orders.GET("", walletFundingHandlers.GetOrders)
			orders.GET("/:id", walletFundingHandlers.GetOrder)
		}

		portfolio := v1.Group("/portfolio")
		portfolio.Use(middleware.Authentication(cfg, log, sessionValidator))
		{
			portfolio.GET("", walletFundingHandlers.GetPortfolio)
		}

		// === WEBHOOK ENDPOINTS (No auth - validated via signature) ===
		webhooks := v1.Group("/webhooks")
		{
			// Chain deposit webhook (from Circle, blockchain nodes, etc.)
			webhooks.POST("/chain-deposit", walletFundingHandlers.ChainDepositWebhook)

			// Brokerage fill webhook (from brokerage partner)
			webhooks.POST("/brokerage-fills", walletFundingHandlers.BrokerageFillWebhook)
		}
	}

	return router
}
