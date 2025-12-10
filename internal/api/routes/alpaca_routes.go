package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterAlpacaRoutes registers Alpaca investment and webhook routes
func RegisterAlpacaRoutes(
	router *gin.RouterGroup,
	investmentHandlers *handlers.InvestmentHandlers,
	webhookHandlers *handlers.AlpacaWebhookHandlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
) {
	// Investment routes (authenticated)
	investment := router.Group("/investment")
	investment.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		// Account management
		investment.GET("/account", investmentHandlers.GetBrokerageAccount)
		investment.POST("/account", investmentHandlers.CreateBrokerageAccount)

		// Funding
		investment.POST("/fund", investmentHandlers.FundBrokerageAccount)
		investment.GET("/funding/limits", investmentHandlers.GetFundingLimits)
		investment.GET("/funding/pending", investmentHandlers.GetPendingFunding)
		investment.GET("/buying-power", investmentHandlers.GetBuyingPower)

		// Positions
		investment.GET("/positions", investmentHandlers.GetPositions)
		investment.POST("/positions/sync", investmentHandlers.SyncPositions)
		investment.POST("/reconcile", investmentHandlers.ReconcilePortfolio)

		// Orders
		investment.POST("/orders", investmentHandlers.PlaceOrder)
		investment.GET("/orders", investmentHandlers.GetOrders)
		investment.GET("/orders/:id", investmentHandlers.GetOrder)
		investment.DELETE("/orders/:id", investmentHandlers.CancelOrder)
	}

	// Webhook routes (no auth - verified by signature)
	webhooks := router.Group("/webhooks/alpaca")
	{
		webhooks.POST("/trade", webhookHandlers.HandleTradeUpdate)
		webhooks.POST("/account", webhookHandlers.HandleAccountUpdate)
		webhooks.POST("/transfer", webhookHandlers.HandleTransferUpdate)
		webhooks.POST("/nta", webhookHandlers.HandleNonTradeActivity)
	}
}
