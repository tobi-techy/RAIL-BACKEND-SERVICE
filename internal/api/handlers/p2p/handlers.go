package p2p

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
)

// Handlers handles P2P transfer API endpoints
type Handlers struct {
	service *p2pservice.Service
	logger  *zap.Logger
}

// NewHandlers creates new P2P handlers
func NewHandlers(service *p2pservice.Service, logger *zap.Logger) *Handlers {
	return &Handlers{service: service, logger: logger}
}

// LookupRequest represents a recipient lookup request
type LookupRequest struct {
	Identifier string `json:"identifier" binding:"required"`
}

// Lookup looks up a recipient by identifier
// POST /api/v1/p2p/lookup
func (h *Handlers) Lookup(c *gin.Context) {
	var req LookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	result, err := h.service.LookupRecipient(c.Request.Context(), req.Identifier)
	if err != nil {
		h.logger.Error("Lookup failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "LOOKUP_FAILED", "message": "Failed to lookup recipient"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Send initiates a P2P transfer
// POST /api/v1/p2p/send
func (h *Handlers) Send(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req entities.P2PSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	result, err := h.service.Send(c.Request.Context(), userID, &req)
	if err != nil {
		h.logger.Error("Send failed", zap.Error(err), zap.String("user_id", userID.String()))
		
		// Map common errors to appropriate responses
		switch err.Error() {
		case "invalid amount":
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_AMOUNT", "message": "Invalid amount"})
		case "insufficient balance":
			c.JSON(http.StatusBadRequest, gin.H{"error": "INSUFFICIENT_BALANCE", "message": "Insufficient balance"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SEND_FAILED", "message": err.Error()})
		}
		return
	}

	c.JSON(http.StatusCreated, result)
}

// GetTransfers returns user's P2P transfers
// GET /api/v1/p2p/transfers
func (h *Handlers) GetTransfers(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	limit := 20
	offset := 0

	transfers, err := h.service.GetTransfers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("GetTransfers failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FETCH_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transfers": transfers})
}

// GetRecentRecipients returns recent recipients for quick access
// GET /api/v1/p2p/recent
func (h *Handlers) GetRecentRecipients(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	recipients, err := h.service.GetRecentRecipients(c.Request.Context(), userID, 10)
	if err != nil {
		h.logger.Error("GetRecentRecipients failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FETCH_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipients": recipients})
}

// Cancel cancels a pending transfer
// DELETE /api/v1/p2p/transfers/:id
func (h *Handlers) Cancel(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	transferID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID"})
		return
	}

	if err := h.service.Cancel(c.Request.Context(), transferID, userID); err != nil {
		h.logger.Error("Cancel failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "CANCEL_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Transfer cancelled"})
}

// ClaimByToken claims a pending transfer (public endpoint for deep links)
// POST /api/v1/p2p/claim/:token
func (h *Handlers) ClaimByToken(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_TOKEN"})
		return
	}

	transfer, err := h.service.ClaimByToken(c.Request.Context(), token, userID)
	if err != nil {
		h.logger.Error("Claim failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "CLAIM_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transfer": transfer, "message": "Transfer claimed!"})
}

// GetClaimInfo returns info about a pending transfer (public, for claim page)
// GET /api/v1/p2p/claim/:token
func (h *Handlers) GetClaimInfo(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_TOKEN"})
		return
	}

	// This would need a repo method to get transfer by token without claiming
	// For now, return minimal info
	c.JSON(http.StatusOK, gin.H{
		"valid":   true,
		"message": "Download Rail to claim your money",
	})
}

func (h *Handlers) getUserID(c *gin.Context) (uuid.UUID, error) {
	userIDStr, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, http.ErrNoCookie
	}
	
	switch v := userIDStr.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		return uuid.Parse(v)
	default:
		return uuid.Nil, http.ErrNoCookie
	}
}

// SetRailTagRequest represents a request to set a RailTag
type SetRailTagRequest struct {
	RailTag string `json:"railTag" binding:"required,min=3,max=30"`
}

// SetRailTag sets the user's RailTag
// POST /api/v1/p2p/railtag
func (h *Handlers) SetRailTag(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req SetRailTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	if err := h.service.SetRailTag(c.Request.Context(), userID, req.RailTag); err != nil {
		h.logger.Error("SetRailTag failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "SET_RAILTAG_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"railTag": req.RailTag, "message": "RailTag set successfully"})
}

// CheckRailTagRequest represents a request to check RailTag availability
type CheckRailTagRequest struct {
	RailTag string `json:"railTag" binding:"required,min=3,max=30"`
}

// CheckRailTag checks if a RailTag is available
// POST /api/v1/p2p/railtag/check
func (h *Handlers) CheckRailTag(c *gin.Context) {
	var req CheckRailTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	available, err := h.service.CheckRailTagAvailable(c.Request.Context(), req.RailTag)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CHECK_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"railTag": req.RailTag, "available": available})
}
