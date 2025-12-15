package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
)

// InvestmentHandlers placeholder for investment route registration
type InvestmentHandlers interface {
	GetBaskets(c *gin.Context)
	InvestInBasket(c *gin.Context)
}

func RegisterInvestmentRoutes(
	router *gin.RouterGroup,
	h InvestmentHandlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
) {
	investment := router.Group("/investment")
	investment.Use(middleware.Authentication(cfg, log, sessionValidator))
	{
		investment.GET("/baskets", h.GetBaskets)
		investment.POST("/baskets/:basket_type/invest", h.InvestInBasket)
	}
}
