package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/pkg/logger"
)

// CopyTradingHandlers handles copy trading API endpoints
type CopyTradingHandlers struct {
	service *copytrading.Service
	logger  *logger.Logger
}

// NewCopyTradingHandlers creates new copy trading handlers
func NewCopyTradingHandlers(service *copytrading.Service, logger *logger.Logger) *CopyTradingHandlers {
	return &CopyTradingHandlers{
		service: service,
		logger:  logger,
	}
}

// ListConductors returns available conductors to follow
// GET /api/v1/copy/conductors
func (h *CopyTradingHandlers) ListConductors(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	sortBy := c.DefaultQuery("sort_by", "followers") // followers, return, aum, win_rate

	result, err := h.service.ListConductors(c.Request.Context(), page, pageSize, sortBy)
	if err != nil {
		h.logger.Error("Failed to list conductors", "error", err)
		respondInternalError(c, "Failed to list conductors")
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetConductor returns detailed conductor information
// GET /api/v1/copy/conductors/:id
func (h *CopyTradingHandlers) GetConductor(c *gin.Context) {
	conductorID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid conductor ID")
		return
	}

	conductor, err := h.service.GetConductor(c.Request.Context(), conductorID)
	if err != nil {
		if err.Error() == "conductor not found" {
			respondNotFound(c, "Conductor not found")
			return
		}
		h.logger.Error("Failed to get conductor", "error", err)
		respondInternalError(c, "Failed to get conductor")
		return
	}

	c.JSON(http.StatusOK, conductor)
}

// GetConductorSignals returns recent signals for a conductor
// GET /api/v1/copy/conductors/:id/signals
func (h *CopyTradingHandlers) GetConductorSignals(c *gin.Context) {
	conductorID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid conductor ID")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	signals, err := h.service.GetConductorSignals(c.Request.Context(), conductorID, limit)
	if err != nil {
		h.logger.Error("Failed to get conductor signals", "error", err)
		respondInternalError(c, "Failed to get signals")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"signals": signals,
		"count":   len(signals),
	})
}

// CreateDraft creates a new copy relationship (follow a conductor)
// POST /api/v1/copy/drafts
func (h *CopyTradingHandlers) CreateDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	var req entities.CreateDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body")
		return
	}

	if req.AllocatedCapital.LessThanOrEqual(decimal.Zero) {
		respondBadRequest(c, "Allocated capital must be positive")
		return
	}

	draft, err := h.service.CreateDraft(c.Request.Context(), userID, &req)
	if err != nil {
		h.logger.Error("Failed to create draft", "error", err, "user_id", userID.String())
		
		// Handle specific errors
		switch err.Error() {
		case "conductor not found":
			respondNotFound(c, "Conductor not found")
		case "conductor is not active":
			respondError(c, http.StatusBadRequest, "CONDUCTOR_INACTIVE", "Conductor is not accepting new followers", nil)
		case "cannot copy your own trades":
			respondError(c, http.StatusBadRequest, "SELF_COPY", "You cannot copy your own trades", nil)
		case "already following this conductor":
			respondError(c, http.StatusConflict, "ALREADY_FOLLOWING", "You are already following this conductor", nil)
		default:
			if len(err.Error()) > 20 && err.Error()[:20] == "minimum allocation is" {
				respondError(c, http.StatusBadRequest, "MIN_ALLOCATION", err.Error(), nil)
			} else if len(err.Error()) > 20 && err.Error()[:20] == "insufficient balance" {
				respondError(c, http.StatusBadRequest, "INSUFFICIENT_BALANCE", err.Error(), nil)
			} else {
				respondInternalError(c, "Failed to create draft")
			}
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Successfully started copying conductor",
		"draft":   draft,
	})
}

// ListUserDrafts returns all drafts for the authenticated user
// GET /api/v1/copy/drafts
func (h *CopyTradingHandlers) ListUserDrafts(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	drafts, err := h.service.GetUserDrafts(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get user drafts", "error", err, "user_id", userID.String())
		respondInternalError(c, "Failed to get drafts")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"drafts": drafts,
		"count":  len(drafts),
	})
}

// GetDraft returns a specific draft with details
// GET /api/v1/copy/drafts/:id
func (h *CopyTradingHandlers) GetDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	draft, err := h.service.GetDraft(c.Request.Context(), userID, draftID)
	if err != nil {
		if err.Error() == "draft not found" {
			respondNotFound(c, "Draft not found")
			return
		}
		if err.Error() == "unauthorized" {
			respondUnauthorized(c, "Not authorized to view this draft")
			return
		}
		h.logger.Error("Failed to get draft", "error", err)
		respondInternalError(c, "Failed to get draft")
		return
	}

	c.JSON(http.StatusOK, draft)
}

// PauseDraft pauses copying for a draft
// POST /api/v1/copy/drafts/:id/pause
func (h *CopyTradingHandlers) PauseDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	if err := h.service.PauseDraft(c.Request.Context(), userID, draftID); err != nil {
		h.logger.Error("Failed to pause draft", "error", err)
		respondError(c, http.StatusBadRequest, "PAUSE_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Draft paused successfully",
	})
}

// ResumeDraft resumes copying for a paused draft
// POST /api/v1/copy/drafts/:id/resume
func (h *CopyTradingHandlers) ResumeDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	if err := h.service.ResumeDraft(c.Request.Context(), userID, draftID); err != nil {
		h.logger.Error("Failed to resume draft", "error", err)
		respondError(c, http.StatusBadRequest, "RESUME_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Draft resumed successfully",
	})
}

// UnlinkDraft stops copying and returns funds
// DELETE /api/v1/copy/drafts/:id
func (h *CopyTradingHandlers) UnlinkDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	if err := h.service.UnlinkDraft(c.Request.Context(), userID, draftID); err != nil {
		h.logger.Error("Failed to unlink draft", "error", err)
		respondError(c, http.StatusBadRequest, "UNLINK_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Draft unlinked successfully. Funds have been returned to your balance.",
	})
}

// ResizeDraft adjusts the allocated capital
// PUT /api/v1/copy/drafts/:id/resize
func (h *CopyTradingHandlers) ResizeDraft(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	var req entities.ResizeDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body")
		return
	}

	if req.NewAllocatedCapital.LessThanOrEqual(decimal.Zero) {
		respondBadRequest(c, "New allocated capital must be positive")
		return
	}

	if err := h.service.ResizeDraft(c.Request.Context(), userID, draftID, req.NewAllocatedCapital); err != nil {
		h.logger.Error("Failed to resize draft", "error", err)
		respondError(c, http.StatusBadRequest, "RESIZE_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Draft allocation updated successfully",
		"new_allocation": req.NewAllocatedCapital.String(),
	})
}

// GetDraftHistory returns execution history for a draft
// GET /api/v1/copy/drafts/:id/history
func (h *CopyTradingHandlers) GetDraftHistory(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	draftID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid draft ID")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	logs, err := h.service.GetDraftExecutionHistory(c.Request.Context(), userID, draftID, limit)
	if err != nil {
		if err.Error() == "draft not found" || err.Error() == "unauthorized" {
			respondNotFound(c, "Draft not found")
			return
		}
		h.logger.Error("Failed to get draft history", "error", err)
		respondInternalError(c, "Failed to get history")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"executions": logs,
		"count":      len(logs),
	})
}
