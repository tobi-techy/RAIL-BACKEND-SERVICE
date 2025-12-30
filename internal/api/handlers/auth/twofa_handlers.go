package auth

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"go.uber.org/zap"
)

// TwoFAHandlers manages two-factor authentication operations
type TwoFAHandlers struct {
	twofaService *twofa.Service
	logger       *zap.Logger
}

// NewTwoFAHandlers creates a new TwoFAHandlers instance
func NewTwoFAHandlers(twofaService *twofa.Service, logger *zap.Logger) *TwoFAHandlers {
	return &TwoFAHandlers{
		twofaService: twofaService,
		logger:       logger,
	}
}

// TwoFACodeRequest represents a request containing a 2FA code
type TwoFACodeRequest struct {
	Code string `json:"code" binding:"required"`
}

// Setup2FA handles POST /api/v1/security/2fa/setup
// Generates a 2FA secret and QR code for the user
func (h *TwoFAHandlers) Setup2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	userEmail := c.GetString("user_email")

	setup, err := h.twofaService.GenerateSecret(ctx, userID, userEmail)
	if err != nil {
		h.logger.Error("Failed to setup 2FA",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FASetupFailed, err.Error())
		return
	}

	common.SendSuccess(c, setup)
}

// Enable2FA handles POST /api/v1/security/2fa/enable
// Enables 2FA after verifying the initial code
func (h *TwoFAHandlers) Enable2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	var req TwoFACodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	if err := h.twofaService.VerifyAndEnable(ctx, userID, req.Code); err != nil {
		h.logger.Error("Failed to enable 2FA",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, err.Error())
		return
	}

	common.SendSuccess(c, gin.H{"message": "2FA enabled successfully"})
}

// Verify2FA handles POST /api/v1/security/2fa/verify
// Verifies a 2FA code for authentication
func (h *TwoFAHandlers) Verify2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	var req TwoFACodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	valid, err := h.twofaService.Verify(ctx, userID, req.Code)
	if err != nil {
		h.logger.Error("Failed to verify 2FA",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, err.Error())
		return
	}

	common.SendSuccess(c, gin.H{"valid": valid})
}

// Disable2FA handles POST /api/v1/security/2fa/disable
// Disables 2FA after verifying the code
func (h *TwoFAHandlers) Disable2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	var req TwoFACodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	if err := h.twofaService.Disable(ctx, userID, req.Code); err != nil {
		h.logger.Error("Failed to disable 2FA",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, err.Error())
		return
	}

	common.SendSuccess(c, gin.H{"message": "2FA disabled successfully"})
}

// Get2FAStatus handles GET /api/v1/security/2fa/status
// Returns the current 2FA status for the user
func (h *TwoFAHandlers) Get2FAStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	status, err := h.twofaService.GetStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get 2FA status",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get 2FA status")
		return
	}

	common.SendSuccess(c, status)
}

// RegenerateBackupCodes handles POST /api/v1/security/2fa/backup-codes
// Regenerates backup codes after verifying the current code
func (h *TwoFAHandlers) RegenerateBackupCodes(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	var req TwoFACodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	codes, err := h.twofaService.RegenerateBackupCodes(ctx, userID, req.Code)
	if err != nil {
		h.logger.Error("Failed to regenerate backup codes",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, err.Error())
		return
	}

	common.SendSuccess(c, gin.H{"backup_codes": codes})
}

// VerifyBackupCode handles POST /api/v1/security/2fa/verify-backup
// Verifies a backup code (one-time use)
func (h *TwoFAHandlers) VerifyBackupCode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	var req TwoFACodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	// Verify as regular code - the service handles backup code detection
	valid, err := h.twofaService.Verify(ctx, userID, req.Code)
	if err != nil {
		h.logger.Error("Failed to verify backup code",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, err.Error())
		return
	}

	if !valid {
		common.SendBadRequest(c, common.ErrCode2FAInvalidCode, "Invalid backup code")
		return
	}

	common.SendSuccess(c, gin.H{
		"valid":   valid,
		"message": "Backup code verified and consumed",
	})
}

// GetRemainingBackupCodes handles GET /api/v1/security/2fa/backup-codes/count
// Returns the count of remaining unused backup codes
func (h *TwoFAHandlers) GetRemainingBackupCodes(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "User ID not found")
		return
	}

	status, err := h.twofaService.GetStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get 2FA status for backup codes",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get backup code count")
		return
	}

	common.SendSuccess(c, gin.H{
		"remaining_backup_codes": status.BackupCodesCount,
		"is_enabled":             status.IsEnabled,
	})
}
