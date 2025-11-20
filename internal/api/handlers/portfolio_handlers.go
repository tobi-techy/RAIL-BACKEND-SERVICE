package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.uber.org/zap"
)

type PortfolioHandler struct {
	portfolioService *services.PortfolioService
	logger           *zap.Logger
}

func NewPortfolioHandler(portfolioService *services.PortfolioService, logger *zap.Logger) *PortfolioHandler {
	return &PortfolioHandler{
		portfolioService: portfolioService,
		logger:           logger,
	}
}

func (h *PortfolioHandler) CalculateRebalance(c *gin.Context) {
	portfolioID := uuid.MustParse(c.Param("id"))

	var req struct {
		Target  map[string]decimal.Decimal `json:"target"`
		Current map[string]decimal.Decimal `json:"current"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trades, err := h.portfolioService.CalculateRebalance(c.Request.Context(), portfolioID, req.Target, req.Current)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"trades": trades})
}

func (h *PortfolioHandler) GenerateTaxReport(c *gin.Context) {
	userID := uuid.MustParse(c.GetString("user_id"))
	yearStr := c.Query("year")

	year, err := strconv.Atoi(yearStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid year"})
		return
	}

	report, err := h.portfolioService.GenerateTaxReport(c.Request.Context(), userID, year)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}
