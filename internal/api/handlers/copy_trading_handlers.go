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

// === Conductor Application Handlers ===

// ApplyAsConductor submits an application to become a conductor
// POST /api/v1/copy/conductors/apply
func (h *CopyTradingHandlers) ApplyAsConductor(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	var req entities.CreateConductorApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	app, err := h.service.ApplyAsConductor(c.Request.Context(), userID, &req)
	if err != nil {
		h.logger.Error("Failed to submit conductor application", "error", err, "user_id", userID.String())
		switch err.Error() {
		case "user must have an existing Rail account":
			respondError(c, http.StatusBadRequest, "NO_ACCOUNT", "You must have an existing Rail account to apply", nil)
		case "user is already a conductor":
			respondError(c, http.StatusConflict, "ALREADY_CONDUCTOR", "You are already a conductor", nil)
		case "application already pending review":
			respondError(c, http.StatusConflict, "APPLICATION_PENDING", "Your application is already pending review", nil)
		default:
			respondInternalError(c, "Failed to submit application")
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":     "Application submitted successfully",
		"application": app,
	})
}

// GetMyApplication returns the current user's conductor application
// GET /api/v1/copy/conductors/application
func (h *CopyTradingHandlers) GetMyApplication(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	app, err := h.service.GetMyApplication(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get application", "error", err)
		respondInternalError(c, "Failed to get application")
		return
	}

	if app == nil {
		respondNotFound(c, "No application found")
		return
	}

	c.JSON(http.StatusOK, app)
}

// ListPendingApplications returns pending conductor applications (admin only)
// GET /api/v1/admin/copy/applications
func (h *CopyTradingHandlers) ListPendingApplications(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	apps, total, err := h.service.ListPendingApplications(c.Request.Context(), page, pageSize)
	if err != nil {
		h.logger.Error("Failed to list applications", "error", err)
		respondInternalError(c, "Failed to list applications")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"applications": apps,
		"total":        total,
		"page":         page,
		"page_size":    pageSize,
	})
}

// ReviewApplication approves or rejects a conductor application (admin only)
// POST /api/v1/admin/copy/applications/:id/review
func (h *CopyTradingHandlers) ReviewApplication(c *gin.Context) {
	reviewerID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	applicationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid application ID")
		return
	}

	var req entities.ReviewConductorApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body")
		return
	}

	if !req.Approved && req.RejectionReason == "" {
		respondBadRequest(c, "Rejection reason is required when rejecting")
		return
	}

	if err := h.service.ReviewApplication(c.Request.Context(), applicationID, reviewerID, &req); err != nil {
		h.logger.Error("Failed to review application", "error", err)
		if err.Error() == "application not found" {
			respondNotFound(c, "Application not found")
			return
		}
		respondError(c, http.StatusBadRequest, "REVIEW_FAILED", err.Error(), nil)
		return
	}

	status := "approved"
	if !req.Approved {
		status = "rejected"
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Application " + status + " successfully",
	})
}

// === Track Handlers ===

// CreateTrack creates a new track for a conductor
// POST /api/v1/copy/tracks
func (h *CopyTradingHandlers) CreateTrack(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	var req entities.CreateTrackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	track, err := h.service.CreateTrack(c.Request.Context(), userID, &req)
	if err != nil {
		h.logger.Error("Failed to create track", "error", err, "user_id", userID.String())
		switch err.Error() {
		case "user is not a conductor":
			respondError(c, http.StatusForbidden, "NOT_CONDUCTOR", "You must be a conductor to create tracks", nil)
		case "conductor account is not active":
			respondError(c, http.StatusForbidden, "CONDUCTOR_INACTIVE", "Your conductor account is not active", nil)
		default:
			if len(err.Error()) > 10 && err.Error()[:10] == "allocations" {
				respondBadRequest(c, err.Error())
			} else {
				respondInternalError(c, "Failed to create track")
			}
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Track created successfully",
		"track":   track,
	})
}

// ListTracks returns available tracks
// GET /api/v1/copy/tracks
func (h *CopyTradingHandlers) ListTracks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	tracks, total, err := h.service.ListTracks(c.Request.Context(), page, pageSize)
	if err != nil {
		h.logger.Error("Failed to list tracks", "error", err)
		respondInternalError(c, "Failed to list tracks")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tracks":    tracks,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetTrack returns a track with its allocations
// GET /api/v1/copy/tracks/:id
func (h *CopyTradingHandlers) GetTrack(c *gin.Context) {
	trackID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid track ID")
		return
	}

	track, err := h.service.GetTrack(c.Request.Context(), trackID)
	if err != nil {
		if err.Error() == "track not found" {
			respondNotFound(c, "Track not found")
			return
		}
		h.logger.Error("Failed to get track", "error", err)
		respondInternalError(c, "Failed to get track")
		return
	}

	c.JSON(http.StatusOK, track)
}

// GetConductorTracks returns all tracks for a conductor
// GET /api/v1/copy/conductors/:id/tracks
func (h *CopyTradingHandlers) GetConductorTracks(c *gin.Context) {
	conductorID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid conductor ID")
		return
	}

	tracks, err := h.service.GetConductorTracks(c.Request.Context(), conductorID)
	if err != nil {
		h.logger.Error("Failed to get conductor tracks", "error", err)
		respondInternalError(c, "Failed to get tracks")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tracks": tracks,
		"count":  len(tracks),
	})
}

// UpdateTrack updates a track's details
// PUT /api/v1/copy/tracks/:id
func (h *CopyTradingHandlers) UpdateTrack(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	trackID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid track ID")
		return
	}

	var req entities.UpdateTrackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request body")
		return
	}

	track, err := h.service.UpdateTrack(c.Request.Context(), userID, trackID, &req)
	if err != nil {
		h.logger.Error("Failed to update track", "error", err)
		if err.Error() == "track not found" {
			respondNotFound(c, "Track not found")
			return
		}
		if err.Error() == "unauthorized to update this track" {
			respondUnauthorized(c, "Not authorized to update this track")
			return
		}
		respondError(c, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Track updated successfully",
		"track":   track,
	})
}

// DeleteTrack deactivates a track
// DELETE /api/v1/copy/tracks/:id
func (h *CopyTradingHandlers) DeleteTrack(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	trackID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "Invalid track ID")
		return
	}

	if err := h.service.DeleteTrack(c.Request.Context(), userID, trackID); err != nil {
		h.logger.Error("Failed to delete track", "error", err)
		if err.Error() == "track not found" {
			respondNotFound(c, "Track not found")
			return
		}
		if err.Error() == "unauthorized to delete this track" {
			respondUnauthorized(c, "Not authorized to delete this track")
			return
		}
		respondError(c, http.StatusBadRequest, "DELETE_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Track deleted successfully",
	})
}
