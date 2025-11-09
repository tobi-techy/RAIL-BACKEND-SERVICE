package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/internal/api/handlers"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stack-service/stack_service/internal/api/middleware"
)

func RegisterInvestmentRoutes(router *gin.RouterGroup, h *handlers.InvestmentHandlers, cfg *config.Config, log *logger.Logger) {
	investment := router.Group("/investment")
	investment.Use(middleware.Authentication(cfg, log))
	{
		investment.GET("/baskets", h.GetBaskets)
		investment.POST("/baskets/:basket_type/invest", h.InvestInBasket)
	}
}
