package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterAdvancedFeaturesRoutes registers analytics, scheduled-investment, and rebalancing routes
func RegisterAdvancedFeaturesRoutes(
	router *gin.RouterGroup,
	analyticsHandlers *handlers.AnalyticsHandlers,
	scheduledInvestmentHandlers *handlers.ScheduledInvestmentHandlers,
	rebalancingHandlers *handlers.RebalancingHandlers,
	financialSnapshotHandler *handlers.FinancialSnapshotHandler,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
	tokenBlacklist *auth.TokenBlacklist,
) {
	// Analytics routes (authenticated)
	analytics := router.Group("/analytics")
	analytics.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
	{
		analytics.GET("/dashboard", analyticsHandlers.GetDashboard)
		analytics.GET("/performance", analyticsHandlers.GetPerformanceMetrics)
		analytics.GET("/risk", analyticsHandlers.GetRiskMetrics)
		analytics.GET("/diversification", analyticsHandlers.GetDiversificationAnalysis)
		analytics.GET("/history", analyticsHandlers.GetPortfolioHistory)
		analytics.POST("/snapshot", analyticsHandlers.TakeSnapshot)
		analytics.GET("/financial-snapshot", financialSnapshotHandler.GetFinancialSnapshot)
	}

	// Scheduled investments routes (authenticated)
	scheduled := router.Group("/scheduled-investments")
	scheduled.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
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
	rebalancing.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
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
	tokenBlacklist *auth.TokenBlacklist,
) {
	if roundupHandlers == nil {
		return
	}

	roundups := router.Group("/roundups")
	roundups.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
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
