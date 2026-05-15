package routes

import (
	"github.com/gin-gonic/gin"
	opportunityhandlers "github.com/rail-service/rail_service/internal/api/handlers/opportunities"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterOpportunityRoutes registers opportunity-related routes.
func RegisterOpportunityRoutes(
	v1 *gin.RouterGroup,
	internalGroup *gin.RouterGroup,
	handlers *opportunityhandlers.Handlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
	tokenBlacklist *auth.TokenBlacklist,
) {
	if handlers == nil {
		log.Warn("Opportunity handlers not initialized, skipping opportunity routes")
		return
	}

	opp := v1.Group("/opportunities")
	opp.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
	{
		opp.GET("/recommendations", handlers.GetRecommendations)
		opp.GET("/profile", handlers.GetProfile)
		opp.POST("/profile", handlers.UpdateProfile)
		opp.GET("/:id", handlers.GetListing)
		opp.POST("/:id/save", handlers.SaveOpportunity)
		opp.POST("/:id/hide", handlers.HideOpportunity)
	}

	if internalGroup != nil {
		internalGroup.POST("/opportunities/sync", handlers.TriggerSync)
	}
}
