package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/pkg/logger"
)

// ScheduledInvestmentHandlers handles scheduled investment endpoints
type ScheduledInvestmentHandlers struct {
	service *investing.ScheduledInvestmentService
	logger  *logger.Logger
}

func NewScheduledInvestmentHandlers(service *investing.ScheduledInvestmentService, logger *logger.Logger) *ScheduledInvestmentHandlers {
	return &ScheduledInvestmentHandlers{service: service, logger: logger}
}

// CreateScheduledInvestment creates a new recurring investment
// POST /api/v1/scheduled-investments
func (h *ScheduledInvestmentHandlers) CreateScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	var req struct {
		Name       *string  `json:"name"`
		Symbol     *string  `json:"symbol"`
		BasketID   *string  `json:"basket_id"`
		Amount     float64  `json:"amount" binding:"required,gt=0"`
		Frequency  string   `json:"frequency" binding:"required"`
		DayOfWeek  *int     `json:"day_of_week"`
		DayOfMonth *int     `json:"day_of_month"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request: "+err.Error())
		return
	}

	createReq := &investing.CreateScheduledInvestmentRequest{
		UserID:     userID,
		Name:       req.Name,
		Symbol:     req.Symbol,
		Amount:     decimal.NewFromFloat(req.Amount),
		Frequency:  req.Frequency,
		DayOfWeek:  req.DayOfWeek,
		DayOfMonth: req.DayOfMonth,
	}

	if req.BasketID != nil {
		basketID, err := uuid.Parse(*req.BasketID)
		if err == nil {
			createReq.BasketID = &basketID
		}
	}

	si, err := h.service.CreateScheduledInvestment(c.Request.Context(), createReq)
	if err != nil {
		h.logger.Error("Failed to create scheduled investment", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, si)
}

// GetScheduledInvestments returns user's scheduled investments
// GET /api/v1/scheduled-investments
func (h *ScheduledInvestmentHandlers) GetScheduledInvestments(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	investments, err := h.service.GetUserScheduledInvestments(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get scheduled investments", "error", err)
		respondInternalError(c, "Failed to get scheduled investments")
		return
	}

	c.JSON(http.StatusOK, gin.H{"scheduled_investments": investments})
}

// GetScheduledInvestment returns a specific scheduled investment
// GET /api/v1/scheduled-investments/:id
func (h *ScheduledInvestmentHandlers) GetScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil {
		h.logger.Error("Failed to get scheduled investment", "error", err)
		respondInternalError(c, "Failed to get scheduled investment")
		return
	}
	if si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}

	// Ownership check
	if si.UserID != userID {
		h.logger.Warn("Unauthorized scheduled investment access", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	c.JSON(http.StatusOK, si)
}

// UpdateScheduledInvestment updates a scheduled investment
// PATCH /api/v1/scheduled-investments/:id
func (h *ScheduledInvestmentHandlers) UpdateScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Verify ownership first
	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil || si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}
	if si.UserID != userID {
		h.logger.Warn("Unauthorized scheduled investment update", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	var req struct {
		Amount    *float64 `json:"amount"`
		Frequency *string  `json:"frequency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request")
		return
	}

	var amount *decimal.Decimal
	if req.Amount != nil {
		a := decimal.NewFromFloat(*req.Amount)
		amount = &a
	}

	if err := h.service.UpdateScheduledInvestment(c.Request.Context(), id, amount, req.Frequency); err != nil {
		h.logger.Error("Failed to update scheduled investment", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Updated"})
}

// PauseScheduledInvestment pauses a scheduled investment
// POST /api/v1/scheduled-investments/:id/pause
func (h *ScheduledInvestmentHandlers) PauseScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Verify ownership first
	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil || si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}
	if si.UserID != userID {
		h.logger.Warn("Unauthorized scheduled investment pause", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	if err := h.service.PauseScheduledInvestment(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to pause scheduled investment", "error", err)
		respondInternalError(c, "Failed to pause")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Paused"})
}

// ResumeScheduledInvestment resumes a paused scheduled investment
// POST /api/v1/scheduled-investments/:id/resume
func (h *ScheduledInvestmentHandlers) ResumeScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Verify ownership first
	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil || si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}
	if si.UserID != userID {
		h.logger.Warn("Unauthorized scheduled investment resume", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	if err := h.service.ResumeScheduledInvestment(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to resume scheduled investment", "error", err)
		respondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Resumed"})
}

// CancelScheduledInvestment cancels a scheduled investment
// DELETE /api/v1/scheduled-investments/:id
func (h *ScheduledInvestmentHandlers) CancelScheduledInvestment(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Verify ownership first
	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil || si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}
	if si.UserID != userID {
		h.logger.Warn("Unauthorized scheduled investment cancel", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	if err := h.service.CancelScheduledInvestment(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to cancel scheduled investment", "error", err)
		respondInternalError(c, "Failed to cancel")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Cancelled"})
}

// GetExecutionHistory returns execution history for a scheduled investment
// GET /api/v1/scheduled-investments/:id/executions
func (h *ScheduledInvestmentHandlers) GetExecutionHistory(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid ID")
		return
	}

	// Verify ownership first
	si, err := h.service.GetScheduledInvestment(c.Request.Context(), id)
	if err != nil || si == nil {
		respondNotFound(c, "Scheduled investment not found")
		return
	}
	if si.UserID != userID {
		h.logger.Warn("Unauthorized execution history access", "user_id", userID.String(), "si_id", id.String())
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this scheduled investment", nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	executions, err := h.service.GetExecutionHistory(c.Request.Context(), id, limit)
	if err != nil {
		h.logger.Error("Failed to get execution history", "error", err)
		respondInternalError(c, "Failed to get execution history")
		return
	}

	c.JSON(http.StatusOK, gin.H{"executions": executions})
}
