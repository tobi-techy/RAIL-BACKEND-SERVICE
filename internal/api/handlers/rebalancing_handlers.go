package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RebalancingHandlers handles portfolio rebalancing endpoints
type RebalancingHandlers struct {
	service *investing.RebalancingService
	logger  *logger.Logger
}

func NewRebalancingHandlers(service *investing.RebalancingService, logger *logger.Logger) *RebalancingHandlers {
	return &RebalancingHandlers{service: service, logger: logger}
}

// CreateRebalancingConfig creates a new rebalancing configuration
// POST /api/v1/rebalancing/configs
func (h *RebalancingHandlers) CreateRebalancingConfig(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	var req struct {
		Name         string             `json:"name" binding:"required"`
		Allocations  map[string]float64 `json:"allocations" binding:"required"`
		ThresholdPct float64            `json:"threshold_pct"`
		Frequency    *string            `json:"frequency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request: "+err.Error())
		return
	}

	allocations := make(map[string]decimal.Decimal)
	for sym, pct := range req.Allocations {
		allocations[sym] = decimal.NewFromFloat(pct)
	}

	thresholdPct := decimal.NewFromFloat(5.0)
	if req.ThresholdPct > 0 {
		thresholdPct = decimal.NewFromFloat(req.ThresholdPct)
	}

	config, err := h.service.CreateRebalancingConfig(c.Request.Context(), userID, req.Name, allocations, thresholdPct, req.Frequency)
	if err != nil {
		h.logger.Error("Failed to create rebalancing config", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, config)
}

// GetRebalancingConfigs returns user's rebalancing configurations
// GET /api/v1/rebalancing/configs
func (h *RebalancingHandlers) GetRebalancingConfigs(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	configs, err := h.service.GetUserConfigs(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get rebalancing configs", "error", err)
		respondInternalError(c, "Failed to get configs")
		return
	}

	c.JSON(http.StatusOK, gin.H{"configs": configs})
}

// GetRebalancingConfig returns a specific rebalancing configuration
// GET /api/v1/rebalancing/configs/:id
func (h *RebalancingHandlers) GetRebalancingConfig(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	config, err := h.service.GetConfig(c.Request.Context(), id)
	if err != nil {
		h.logger.Error("Failed to get rebalancing config", "error", err)
		respondInternalError(c, "Failed to get config")
		return
	}
	if config == nil {
		respondNotFound(c, "Config not found")
		return
	}

	c.JSON(http.StatusOK, config)
}

// UpdateRebalancingConfig updates a rebalancing configuration
// PATCH /api/v1/rebalancing/configs/:id
func (h *RebalancingHandlers) UpdateRebalancingConfig(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Allocations  map[string]float64 `json:"allocations"`
		ThresholdPct *float64           `json:"threshold_pct"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request")
		return
	}

	var allocations map[string]decimal.Decimal
	if req.Allocations != nil {
		allocations = make(map[string]decimal.Decimal)
		for sym, pct := range req.Allocations {
			allocations[sym] = decimal.NewFromFloat(pct)
		}
	}

	var thresholdPct *decimal.Decimal
	if req.ThresholdPct != nil {
		t := decimal.NewFromFloat(*req.ThresholdPct)
		thresholdPct = &t
	}

	if err := h.service.UpdateConfig(c.Request.Context(), id, allocations, thresholdPct); err != nil {
		h.logger.Error("Failed to update rebalancing config", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Updated"})
}

// DeleteRebalancingConfig deletes a rebalancing configuration
// DELETE /api/v1/rebalancing/configs/:id
func (h *RebalancingHandlers) DeleteRebalancingConfig(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	if err := h.service.DeleteConfig(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to delete rebalancing config", "error", err)
		respondInternalError(c, "Failed to delete config")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

// GenerateRebalancingPlan generates a rebalancing plan
// GET /api/v1/rebalancing/configs/:id/plan
func (h *RebalancingHandlers) GenerateRebalancingPlan(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	configID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	plan, err := h.service.GenerateRebalancingPlan(c.Request.Context(), userID, configID)
	if err != nil {
		h.logger.Error("Failed to generate rebalancing plan", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, plan)
}

// ExecuteRebalancing executes a rebalancing plan
// POST /api/v1/rebalancing/configs/:id/execute
func (h *RebalancingHandlers) ExecuteRebalancing(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	configID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Generate plan first
	plan, err := h.service.GenerateRebalancingPlan(c.Request.Context(), userID, configID)
	if err != nil {
		h.logger.Error("Failed to generate rebalancing plan", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	if len(plan.Trades) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "Portfolio already balanced", "trades": 0})
		return
	}

	// Execute the plan
	if err := h.service.ExecuteRebalancingPlan(c.Request.Context(), userID, plan); err != nil {
		h.logger.Error("Failed to execute rebalancing", "error", err)
		respondInternalError(c, "Failed to execute rebalancing: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Rebalancing executed", "trades": len(plan.Trades)})
}

// CheckDrift checks if portfolio has drifted beyond threshold
// GET /api/v1/rebalancing/configs/:id/drift
func (h *RebalancingHandlers) CheckDrift(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	configID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	needsRebalance, maxDrift, err := h.service.CheckDrift(c.Request.Context(), userID, configID)
	if err != nil {
		h.logger.Error("Failed to check drift", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"needs_rebalance": needsRebalance,
		"max_drift_pct":   maxDrift.StringFixed(2),
	})
}
