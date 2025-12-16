package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterCardRoutes registers card-related routes
func RegisterCardRoutes(
	v1 *gin.RouterGroup,
	cardHandlers *handlers.CardHandlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
) {
	if cardHandlers == nil {
		log.Warn("Card handlers not initialized, skipping card routes")
		return
	}

	// Protected card routes
	cards := v1.Group("/cards")
	cards.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		// List all user cards
		cards.GET("", cardHandlers.GetCards)
		
		// Create a new card (virtual)
		cards.POST("", cardHandlers.CreateCard)
		
		// Get all card transactions for user
		cards.GET("/transactions", cardHandlers.GetAllTransactions)
		
		// Get specific card
		cards.GET("/:id", cardHandlers.GetCard)
		
		// Freeze card
		cards.POST("/:id/freeze", cardHandlers.FreezeCard)
		
		// Unfreeze card
		cards.POST("/:id/unfreeze", cardHandlers.UnfreezeCard)
		
		// Get card transactions
		cards.GET("/:id/transactions", cardHandlers.GetCardTransactions)
	}
}
