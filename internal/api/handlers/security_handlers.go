package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"go.uber.org/zap"
)

type SecurityEnhancedHandlers struct {
	deviceService     *security.DeviceTrackingService
	ipWhitelistSvc    *security.IPWhitelistService
	withdrawalSecSvc  *security.WithdrawalSecurityService
	eventLogger       *security.SecurityEventLogger
	logger            *zap.Logger
}

func NewSecurityEnhancedHandlers(
	deviceService *security.DeviceTrackingService,
	ipWhitelistSvc *security.IPWhitelistService,
	withdrawalSecSvc *security.WithdrawalSecurityService,
	eventLogger *security.SecurityEventLogger,
	logger *zap.Logger,
) *SecurityEnhancedHandlers {
	return &SecurityEnhancedHandlers{
		deviceService:    deviceService,
		ipWhitelistSvc:   ipWhitelistSvc,
		withdrawalSecSvc: withdrawalSecSvc,
		eventLogger:      eventLogger,
		logger:           logger,
	}
}

// GetDevices returns user's known devices
func (h *SecurityEnhancedHandlers) GetDevices(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	devices, err := h.deviceService.GetUserDevices(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get devices", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

// TrustDevice marks a device as trusted
func (h *SecurityEnhancedHandlers) TrustDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_DEVICE_ID"})
		return
	}

	if err := h.deviceService.TrustDevice(c.Request.Context(), userID, deviceID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Device trusted"})
}

// RevokeDevice removes a device
func (h *SecurityEnhancedHandlers) RevokeDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_DEVICE_ID"})
		return
	}

	if err := h.deviceService.RevokeDevice(c.Request.Context(), userID, deviceID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Device revoked"})
}

// GetIPWhitelist returns user's whitelisted IPs
func (h *SecurityEnhancedHandlers) GetIPWhitelist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	ips, err := h.ipWhitelistSvc.GetUserWhitelist(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get IP whitelist", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"whitelist": ips})
}

// AddIPToWhitelist adds an IP to whitelist
func (h *SecurityEnhancedHandlers) AddIPToWhitelist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		IPAddress string `json:"ip_address" binding:"required"`
		Label     string `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	ip, err := h.ipWhitelistSvc.AddIP(c.Request.Context(), userID, req.IPAddress, req.Label)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Log security event
	h.eventLogger.Log(c.Request.Context(), &security.SecurityEvent{
		UserID:    &userID,
		EventType: security.EventIPWhitelistAdd,
		Severity:  security.SeverityInfo,
		IPAddress: c.ClientIP(),
		Metadata:  map[string]interface{}{"added_ip": req.IPAddress},
	})

	c.JSON(http.StatusCreated, gin.H{
		"ip":      ip,
		"message": "IP added. Verification required to activate.",
	})
}

// VerifyWhitelistedIP verifies and activates a whitelisted IP
func (h *SecurityEnhancedHandlers) VerifyWhitelistedIP(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	ipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_IP_ID"})
		return
	}

	if err := h.ipWhitelistSvc.VerifyIP(c.Request.Context(), userID, ipID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "IP verified and activated"})
}

// RemoveIPFromWhitelist removes an IP from whitelist
func (h *SecurityEnhancedHandlers) RemoveIPFromWhitelist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	ipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_IP_ID"})
		return
	}

	if err := h.ipWhitelistSvc.RemoveIP(c.Request.Context(), userID, ipID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	h.eventLogger.Log(c.Request.Context(), &security.SecurityEvent{
		UserID:    &userID,
		EventType: security.EventIPWhitelistRemove,
		Severity:  security.SeverityInfo,
		IPAddress: c.ClientIP(),
	})

	c.JSON(http.StatusOK, gin.H{"message": "IP removed from whitelist"})
}

// GetSecurityEvents returns user's security events
func (h *SecurityEnhancedHandlers) GetSecurityEvents(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	events, err := h.eventLogger.GetUserSecurityEvents(c.Request.Context(), userID, 50)
	if err != nil {
		h.logger.Error("Failed to get security events", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"events": events})
}

// ConfirmWithdrawal confirms a pending withdrawal
func (h *SecurityEnhancedHandlers) ConfirmWithdrawal(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	confirmation, err := h.withdrawalSecSvc.VerifyConfirmation(c.Request.Context(), req.Token, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.eventLogger.Log(c.Request.Context(), &security.SecurityEvent{
		UserID:    &userID,
		EventType: security.EventWithdrawalConfirm,
		Severity:  security.SeverityInfo,
		IPAddress: c.ClientIP(),
		Metadata: map[string]interface{}{
			"withdrawal_id": confirmation.WithdrawalID,
			"amount":        confirmation.Amount.String(),
		},
	})

	c.JSON(http.StatusOK, gin.H{
		"confirmed":     true,
		"withdrawal_id": confirmation.WithdrawalID,
	})
}

// GetCurrentIP returns the client's current IP
func (h *SecurityEnhancedHandlers) GetCurrentIP(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ip": c.ClientIP()})
}
