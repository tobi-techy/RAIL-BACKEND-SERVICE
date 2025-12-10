package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterAdvancedFeaturesRoutes registers analytics, market data, and automation routes
func RegisterAdvancedFeaturesRoutes(
	router *gin.RouterGroup,
	analyticsHandlers *handlers.AnalyticsHandlers,
	marketHandlers *handlers.MarketHandlers,
	scheduledInvestmentHandlers *handlers.ScheduledInvestmentHandlers,
	rebalancingHandlers *handlers.RebalancingHandlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
) {
	// Analytics routes (authenticated)
	analytics := router.Group("/analytics")
	analytics.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		analytics.GET("/performance", analyticsHandlers.GetPerformanceMetrics)
		analytics.GET("/risk", analyticsHandlers.GetRiskMetrics)
		analytics.GET("/diversification", analyticsHandlers.GetDiversificationAnalysis)
		analytics.POST("/snapshot", analyticsHandlers.TakeSnapshot)
	}

	// Market data routes (mixed auth)
	market := router.Group("/market")
	{
		// Public endpoints
		market.GET("/quote/:symbol", marketHandlers.GetQuote)
		market.GET("/quotes", marketHandlers.GetQuotes)
		market.GET("/bars/:symbol", marketHandlers.GetBars)

		// Authenticated endpoints for alerts
		alerts := market.Group("/alerts")
		alerts.Use(middleware.Authentication(cfg, log, sessionValidator))
		{
			alerts.POST("", marketHandlers.CreateAlert)
			alerts.GET("", marketHandlers.GetAlerts)
			alerts.DELETE("/:id", marketHandlers.DeleteAlert)
		}
	}

	// Scheduled investments routes (authenticated)
	scheduled := router.Group("/scheduled-investments")
	scheduled.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		scheduled.POST("", scheduledInvestmentHandlers.CreateScheduledInvestment)
		scheduled.GET("", scheduledInvestmentHandlers.GetScheduledInvestments)
		scheduled.GET("/:id", scheduledInvestmentHandlers.GetScheduledInvestment)
		scheduled.PATCH("/:id", scheduledInvestmentHandlers.UpdateScheduledInvestment)
		scheduled.DELETE("/:id", scheduledInvestmentHandlers.CancelScheduledInvestment)
		scheduled.POST("/:id/pause", scheduledInvestmentHandlers.PauseScheduledInvestment)
		scheduled.POST("/:id/resume", scheduledInvestmentHandlers.ResumeScheduledInvestment)
		scheduled.GET("/:id/executions", scheduledInvestmentHandlers.GetExecutionHistory)
	}

	// Rebalancing routes (authenticated)
	rebalancing := router.Group("/rebalancing")
	rebalancing.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		rebalancing.POST("/configs", rebalancingHandlers.CreateRebalancingConfig)
		rebalancing.GET("/configs", rebalancingHandlers.GetRebalancingConfigs)
		rebalancing.GET("/configs/:id", rebalancingHandlers.GetRebalancingConfig)
		rebalancing.PATCH("/configs/:id", rebalancingHandlers.UpdateRebalancingConfig)
		rebalancing.DELETE("/configs/:id", rebalancingHandlers.DeleteRebalancingConfig)
		rebalancing.GET("/configs/:id/plan", rebalancingHandlers.GenerateRebalancingPlan)
		rebalancing.POST("/configs/:id/execute", rebalancingHandlers.ExecuteRebalancing)
		rebalancing.GET("/configs/:id/drift", rebalancingHandlers.CheckDrift)
	}
}

// RegisterRoundupRoutes registers round-up routes
func RegisterRoundupRoutes(
	router *gin.RouterGroup,
	roundupHandlers *handlers.RoundupHandlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
) {
	if roundupHandlers == nil {
		return
	}

	roundups := router.Group("/roundups")
	roundups.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		roundups.GET("/settings", roundupHandlers.GetSettings)
		roundups.PUT("/settings", roundupHandlers.UpdateSettings)
		roundups.GET("/summary", roundupHandlers.GetSummary)
		roundups.GET("/transactions", roundupHandlers.GetTransactions)
		roundups.POST("/transactions", roundupHandlers.ProcessTransaction)
		roundups.POST("/preview", roundupHandlers.CalculatePreview)
		roundups.POST("/collect", roundupHandlers.CollectRoundups)
	}
}
