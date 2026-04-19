package security

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	securitySvc "github.com/rail-service/rail_service/internal/domain/services/security"
)

type SecurityFeaturesHandler struct {
	whitelistSvc *securitySvc.AddressWhitelistService
	mfaSvc       *securitySvc.AdaptiveMFAService
	logger       *zap.Logger
}

func NewSecurityFeaturesHandler(
	whitelistSvc *securitySvc.AddressWhitelistService,
	mfaSvc *securitySvc.AdaptiveMFAService,
	logger *zap.Logger,
) *SecurityFeaturesHandler {
	return &SecurityFeaturesHandler{
		whitelistSvc: whitelistSvc,
		mfaSvc:       mfaSvc,
		logger:       logger,
	}
}

// === Whitelist Endpoints ===

func (h *SecurityFeaturesHandler) AddWhitelistAddress(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		Chain   string `json:"chain" binding:"required"`
		Address string `json:"address" binding:"required"`
		Label   string `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	addr, err := h.whitelistSvc.AddAddress(c.Request.Context(), userID, req.Chain, req.Address, req.Label)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"address": addr})
}

func (h *SecurityFeaturesHandler) GetWhitelistAddresses(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	addrs, err := h.whitelistSvc.GetAddresses(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get whitelist", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"addresses": addrs})
}

func (h *SecurityFeaturesHandler) RemoveWhitelistAddress(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	addrID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID"})
		return
	}

	if err := h.whitelistSvc.RemoveAddress(c.Request.Context(), addrID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Address removed"})
}

// === MFA Endpoints ===

func (h *SecurityFeaturesHandler) RequestMFAChallenge(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		ChallengeType entities.MFAChallengeType `json:"challenge_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	resp, code, err := h.mfaSvc.CreateChallenge(c.Request.Context(), userID, req.ChallengeType)
	if err != nil {
		h.logger.Error("Failed to create MFA challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// In production, send code via email/SMS. For now return challenge metadata.
	// The code would be sent via notification service.
	_ = code

	c.JSON(http.StatusOK, gin.H{"challenge": resp})
}

func (h *SecurityFeaturesHandler) VerifyMFAChallenge(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		ChallengeType entities.MFAChallengeType `json:"challenge_type" binding:"required"`
		Code          string                    `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	valid, err := h.mfaSvc.VerifyChallenge(c.Request.Context(), userID, req.ChallengeType, req.Code)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CODE", "verified": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"verified": true})
}
