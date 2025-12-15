package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"go.uber.org/zap"
)

// PasscodeHandlers manages passcode operations
type PasscodeHandlers struct {
	passcodeService *passcode.Service
	logger          *zap.Logger
}

// NewPasscodeHandlers creates a new PasscodeHandlers instance
func NewPasscodeHandlers(passcodeService *passcode.Service, logger *zap.Logger) *PasscodeHandlers {
	return &PasscodeHandlers{
		passcodeService: passcodeService,
		logger:          logger,
	}
}

// SetPasscodeRequest represents a request to set or change passcode
type SetPasscodeRequest struct {
	Passcode string `json:"passcode" binding:"required"`
}

// VerifyPasscodeRequest represents a request to verify passcode
type VerifyPasscodeRequest struct {
	Passcode string `json:"passcode" binding:"required"`
}

// ChangePasscodeRequest represents a request to change passcode
type ChangePasscodeRequest struct {
	CurrentPasscode string `json:"current_passcode" binding:"required"`
	NewPasscode     string `json:"new_passcode" binding:"required"`
}

// SetPasscode handles POST /api/v1/security/passcode/set
// Sets a new passcode for the user
func (h *PasscodeHandlers) SetPasscode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	var req SetPasscodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	if !isValidPasscodeFormat(req.Passcode) {
		SendBadRequest(c, ErrCodePasscodeInvalid, "Passcode must be 4 digits")
		return
	}

	_, err = h.passcodeService.SetPasscode(ctx, userID, req.Passcode)
	if err != nil {
		h.logger.Error("Failed to set passcode",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		switch err {
		case passcode.ErrPasscodeAlreadySet:
			SendConflict(c, ErrCodePasscodeExists, "Passcode already set. Use change endpoint instead.")
		case passcode.ErrPasscodeInvalidFormat:
			SendBadRequest(c, ErrCodePasscodeInvalid, err.Error())
		default:
			SendInternalError(c, ErrCodeInternalError, "Failed to set passcode")
		}
		return
	}

	SendSuccess(c, gin.H{"message": "Passcode set successfully"})
}

// VerifyPasscode handles POST /api/v1/security/passcode/verify
// Verifies the user's passcode
func (h *PasscodeHandlers) VerifyPasscode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	var req VerifyPasscodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	token, expiresAt, err := h.passcodeService.VerifyPasscode(ctx, userID, req.Passcode)
	if err != nil {
		h.logger.Error("Failed to verify passcode",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		switch err {
		case passcode.ErrPasscodeNotSet:
			SendNotFound(c, ErrCodePasscodeNotSet, "Passcode not set")
		case passcode.ErrPasscodeLocked:
			SendLocked(c, ErrCodePasscodeLocked, "Passcode locked due to too many attempts")
		case passcode.ErrPasscodeMismatch:
			SendBadRequest(c, ErrCodePasscodeMismatch, "Invalid passcode")
		default:
			SendInternalError(c, ErrCodeInternalError, "Failed to verify passcode")
		}
		return
	}

	SendSuccess(c, gin.H{
		"valid":      true,
		"token":      token,
		"expires_at": expiresAt,
	})
}

// ChangePasscode handles POST /api/v1/security/passcode/change
// Changes the user's passcode
func (h *PasscodeHandlers) ChangePasscode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	var req ChangePasscodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	if !isValidPasscodeFormat(req.NewPasscode) {
		SendBadRequest(c, ErrCodePasscodeInvalid, "New passcode must be 4 digits")
		return
	}

	if req.CurrentPasscode == req.NewPasscode {
		SendBadRequest(c, ErrCodePasscodeUnchanged, "New passcode must be different from current")
		return
	}

	_, err = h.passcodeService.UpdatePasscode(ctx, userID, req.CurrentPasscode, req.NewPasscode)
	if err != nil {
		h.logger.Error("Failed to change passcode",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		switch err {
		case passcode.ErrPasscodeNotSet:
			SendNotFound(c, ErrCodePasscodeNotSet, "Passcode not set")
		case passcode.ErrPasscodeMismatch:
			SendBadRequest(c, ErrCodePasscodeMismatch, "Invalid current passcode")
		case passcode.ErrPasscodeLocked:
			SendLocked(c, ErrCodePasscodeLocked, "Passcode locked due to too many attempts")
		case passcode.ErrPasscodeSameAsCurrent:
			SendBadRequest(c, ErrCodePasscodeUnchanged, "New passcode must be different from current")
		default:
			SendInternalError(c, ErrCodeInternalError, "Failed to change passcode")
		}
		return
	}

	SendSuccess(c, gin.H{"message": "Passcode changed successfully"})
}

// RemovePasscode handles POST /api/v1/security/passcode/remove
// Removes the user's passcode after verification
func (h *PasscodeHandlers) RemovePasscode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	var req VerifyPasscodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	_, err = h.passcodeService.RemovePasscode(ctx, userID, req.Passcode)
	if err != nil {
		h.logger.Error("Failed to remove passcode",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		switch err {
		case passcode.ErrPasscodeNotSet:
			SendNotFound(c, ErrCodePasscodeNotSet, "Passcode not set")
		case passcode.ErrPasscodeMismatch:
			SendBadRequest(c, ErrCodePasscodeMismatch, "Invalid passcode")
		case passcode.ErrPasscodeLocked:
			SendLocked(c, ErrCodePasscodeLocked, "Passcode locked due to too many attempts")
		default:
			SendInternalError(c, ErrCodeInternalError, "Failed to remove passcode")
		}
		return
	}

	SendSuccess(c, gin.H{"message": "Passcode removed successfully"})
}

// GetPasscodeStatus handles GET /api/v1/security/passcode/status
// Returns the passcode status for the user
func (h *PasscodeHandlers) GetPasscodeStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	status, err := h.passcodeService.GetStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get passcode status",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to get passcode status")
		return
	}

	SendSuccess(c, status)
}

// isValidPasscodeFormat validates that the passcode is 4 digits
func isValidPasscodeFormat(passcodeStr string) bool {
	if len(passcodeStr) != 4 {
		return false
	}
	for _, c := range passcodeStr {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
