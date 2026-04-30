package p2p

import (
	"errors"
	"net/http"
	"strconv"

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
		switch {
		case errors.Is(err, entities.ErrP2PInvalidAmount):
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_AMOUNT", "message": "Invalid amount"})
		case errors.Is(err, entities.ErrP2PInsufficientFunds):
			c.JSON(http.StatusBadRequest, gin.H{"error": "INSUFFICIENT_BALANCE", "message": "Insufficient balance"})
		case errors.Is(err, entities.ErrP2PAmountTooLow):
			c.JSON(http.StatusBadRequest, gin.H{"error": "AMOUNT_TOO_LOW", "message": "Amount below minimum transfer limit"})
		case errors.Is(err, entities.ErrP2PAmountTooHigh):
			c.JSON(http.StatusBadRequest, gin.H{"error": "AMOUNT_TOO_HIGH", "message": "Amount exceeds maximum transfer limit"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SEND_FAILED", "message": "An error occurred while processing your request"})
		}
		return
	}

	c.JSON(http.StatusCreated, result)
}

// GetTransfers returns user's P2P transfers
// GET /api/v1/p2p/transfers?limit=20&offset=0
func (h *Handlers) GetTransfers(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	limit := 20
	offset := 0

	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.ParseUint(limitStr, 10, 64); err == nil && parsed > 0 && parsed <= 100 {
			limit = int(parsed)
		}
	}
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if parsed, err := strconv.ParseUint(offsetStr, 10, 64); err == nil && parsed >= 0 {
			offset = int(parsed)
		}
	}

	transfers, err := h.service.GetTransfers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("GetTransfers failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FETCH_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transfers": transfers, "limit": limit, "offset": offset})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "CANCEL_FAILED", "message": "Unable to cancel transfer"})
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

	info, err := h.service.GetClaimInfo(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"amount":      info.Amount.StringFixed(2),
		"currency":    info.Currency,
		"sender_name": info.SenderName,
		"note":        info.Note,
	})
}

// ClaimToBankRequest is the HTTP request body for bank claim
type ClaimToBankRequest struct {
	AccountHolderName string `json:"account_holder_name" binding:"required,min=2"`
	RoutingNumber     string `json:"routing_number"      binding:"required,len=9"`
	AccountNumber     string `json:"account_number"      binding:"required,min=4,max=17"`
}

// ClaimToBank pays out a pending transfer to the recipient's bank (no app required)
// POST /api/v1/p2p/claim/:token/bank
// SECURITY: Per-token rate limit (3 attempts) prevents brute-force bank detail submission.
func (h *Handlers) ClaimToBank(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_TOKEN"})
		return
	}

	var req ClaimToBankRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "Invalid bank details"})
		return
	}

	err := h.service.ClaimToBank(c.Request.Context(), token, p2pservice.ClaimToBankRequest{
		AccountHolderName: req.AccountHolderName,
		RoutingNumber:     req.RoutingNumber,
		AccountNumber:     req.AccountNumber,
	})
	if err != nil {
		h.logger.Error("ClaimToBank failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "CLAIM_FAILED", "message": "Unable to process bank claim. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Your money is on its way. Expect it in 1-3 business days."})
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
