package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Error codes as constants for consistent error responses across handlers
const (
	// Authentication & Authorization errors
	ErrCodeUnauthorized        = "UNAUTHORIZED"
	ErrCodeForbidden           = "FORBIDDEN"
	ErrCodeInvalidCredentials  = "INVALID_CREDENTIALS"
	ErrCodeInvalidToken        = "INVALID_TOKEN"
	ErrCodeTokenExpired        = "TOKEN_EXPIRED"
	ErrCodeAccountInactive     = "ACCOUNT_INACTIVE"
	ErrCodeAdminRequired       = "ADMIN_PRIVILEGES_REQUIRED"

	// Validation errors
	ErrCodeInvalidRequest      = "INVALID_REQUEST"
	ErrCodeValidationError     = "VALIDATION_ERROR"
	ErrCodeInvalidID           = "INVALID_ID"
	ErrCodeInvalidUserID       = "INVALID_USER_ID"
	ErrCodeInvalidEmail        = "INVALID_EMAIL"
	ErrCodeInvalidPhone        = "INVALID_PHONE"
	ErrCodeInvalidChain        = "INVALID_CHAIN"
	ErrCodeInvalidAmount       = "INVALID_AMOUNT"
	ErrCodeInvalidStatus       = "INVALID_STATUS"
	ErrCodeInvalidRole         = "INVALID_ROLE"
	ErrCodeMissingField        = "MISSING_FIELD"

	// Resource errors
	ErrCodeNotFound            = "NOT_FOUND"
	ErrCodeUserNotFound        = "USER_NOT_FOUND"
	ErrCodeWalletNotFound      = "WALLET_NOT_FOUND"
	ErrCodeOrderNotFound       = "ORDER_NOT_FOUND"
	ErrCodeBasketNotFound      = "BASKET_NOT_FOUND"
	ErrCodeWithdrawalNotFound  = "WITHDRAWAL_NOT_FOUND"
	ErrCodeAlreadyExists       = "ALREADY_EXISTS"
	ErrCodeUserExists          = "USER_EXISTS"
	ErrCodeConflict            = "CONFLICT"

	// Operation errors
	ErrCodeInternalError       = "INTERNAL_ERROR"
	ErrCodeCreateFailed        = "CREATE_FAILED"
	ErrCodeUpdateFailed        = "UPDATE_FAILED"
	ErrCodeDeleteFailed        = "DELETE_FAILED"
	ErrCodeOperationFailed     = "OPERATION_FAILED"
	ErrCodeServiceUnavailable  = "SERVICE_UNAVAILABLE"

	// Authentication specific
	ErrCodeInvalidCode         = "INVALID_CODE"
	ErrCodeCodeExpired         = "CODE_EXPIRED"
	ErrCodeTooManyAttempts     = "TOO_MANY_ATTEMPTS"
	ErrCodeWeakPassword        = "WEAK_PASSWORD"
	ErrCodePasswordHashFailed  = "PASSWORD_HASH_FAILED"
	ErrCodeTokenGenFailed      = "TOKEN_GENERATION_FAILED"

	// Passcode errors
	ErrCodePasscodeNotSet      = "PASSCODE_NOT_SET"
	ErrCodePasscodeExists      = "PASSCODE_EXISTS"
	ErrCodePasscodeMismatch    = "PASSCODE_MISMATCH"
	ErrCodePasscodeLocked      = "PASSCODE_LOCKED"
	ErrCodePasscodeInvalid     = "INVALID_PASSCODE_FORMAT"
	ErrCodePasscodeUnchanged   = "PASSCODE_UNCHANGED"

	// 2FA errors
	ErrCode2FAUnavailable      = "2FA_UNAVAILABLE"
	ErrCode2FASetupFailed      = "2FA_SETUP_FAILED"
	ErrCode2FAInvalidCode      = "2FA_INVALID_CODE"

	// Funding & Wallet errors
	ErrCodeInsufficientFunds   = "INSUFFICIENT_FUNDS"
	ErrCodeInsufficientPosition = "INSUFFICIENT_POSITION"
	ErrCodeWalletCreationFailed = "WALLET_CREATION_FAILED"
	ErrCodeDepositFailed       = "DEPOSIT_FAILED"
	ErrCodeWithdrawalFailed    = "WITHDRAWAL_FAILED"
	ErrCodeProvisioningFailed  = "PROVISIONING_FAILED"

	// KYC errors
	ErrCodeKYCUnavailable      = "KYC_UNAVAILABLE"
	ErrCodeKYCNotEligible      = "KYC_NOT_ELIGIBLE"
	ErrCodeKYCSubmissionFailed = "KYC_SUBMISSION_FAILED"

	// Onboarding errors
	ErrCodeOnboardingFailed    = "ONBOARDING_FAILED"
	ErrCodeRegistrationNotFound = "REGISTRATION_NOT_FOUND"
	ErrCodeAlreadyVerified     = "ALREADY_VERIFIED"

	// Webhook errors
	ErrCodeInvalidSignature    = "INVALID_SIGNATURE"
	ErrCodeWebhookFailed       = "WEBHOOK_PROCESSING_ERROR"
)

// Error messages as constants for consistency
const (
	MsgInvalidRequest       = "Invalid request payload"
	MsgUnauthorized         = "Authentication required"
	MsgForbidden            = "Insufficient permissions"
	MsgInternalError        = "Internal server error"
	MsgUserNotFound         = "User not found"
	MsgInvalidCredentials   = "Invalid email or password"
	MsgServiceUnavailable   = "Service temporarily unavailable"
)

// ErrorResponseBuilder provides a fluent interface for building error responses
type ErrorResponseBuilder struct {
	status  int
	code    string
	message string
	details map[string]interface{}
}

// NewError creates a new ErrorResponseBuilder
func NewError(status int, code string) *ErrorResponseBuilder {
	return &ErrorResponseBuilder{
		status: status,
		code:   code,
	}
}

// Message sets the error message
func (e *ErrorResponseBuilder) Message(msg string) *ErrorResponseBuilder {
	e.message = msg
	return e
}

// Detail adds a single detail to the error response
func (e *ErrorResponseBuilder) Detail(key string, value interface{}) *ErrorResponseBuilder {
	if e.details == nil {
		e.details = make(map[string]interface{})
	}
	e.details[key] = value
	return e
}

// Details sets all details at once
func (e *ErrorResponseBuilder) Details(details map[string]interface{}) *ErrorResponseBuilder {
	e.details = details
	return e
}

// Send sends the error response
func (e *ErrorResponseBuilder) Send(c *gin.Context) {
	c.JSON(e.status, entities.ErrorResponse{
		Code:    e.code,
		Message: e.message,
		Details: e.details,
	})
}

// Common error response helpers for frequently used errors

// SendBadRequest sends a 400 Bad Request error
func SendBadRequest(c *gin.Context, code, message string, details ...map[string]interface{}) {
	var det map[string]interface{}
	if len(details) > 0 {
		det = details[0]
	}
	c.JSON(http.StatusBadRequest, entities.ErrorResponse{
		Code:    code,
		Message: message,
		Details: det,
	})
}

// SendUnauthorized sends a 401 Unauthorized error
func SendUnauthorized(c *gin.Context, message string) {
	c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
		Code:    ErrCodeUnauthorized,
		Message: message,
	})
}

// SendForbidden sends a 403 Forbidden error
func SendForbidden(c *gin.Context, message string) {
	c.JSON(http.StatusForbidden, entities.ErrorResponse{
		Code:    ErrCodeForbidden,
		Message: message,
	})
}

// SendNotFound sends a 404 Not Found error
func SendNotFound(c *gin.Context, code, message string) {
	c.JSON(http.StatusNotFound, entities.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// SendConflict sends a 409 Conflict error
func SendConflict(c *gin.Context, code, message string) {
	c.JSON(http.StatusConflict, entities.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// SendInternalError sends a 500 Internal Server Error
func SendInternalError(c *gin.Context, code, message string) {
	c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// SendServiceUnavailable sends a 503 Service Unavailable error
func SendServiceUnavailable(c *gin.Context, message string) {
	c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
		Code:    ErrCodeServiceUnavailable,
		Message: message,
	})
}

// SendTooManyRequests sends a 429 Too Many Requests error
func SendTooManyRequests(c *gin.Context, message string) {
	c.JSON(http.StatusTooManyRequests, entities.ErrorResponse{
		Code:    ErrCodeTooManyAttempts,
		Message: message,
	})
}

// SendLocked sends a 423 Locked error
func SendLocked(c *gin.Context, code, message string) {
	c.JSON(http.StatusLocked, entities.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// SendSuccess sends a 200 OK response with data
func SendSuccess(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, data)
}

// SendCreated sends a 201 Created response with data
func SendCreated(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, data)
}

// SendAccepted sends a 202 Accepted response with data
func SendAccepted(c *gin.Context, data interface{}) {
	c.JSON(http.StatusAccepted, data)
}

// SendNoContent sends a 204 No Content response
func SendNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Validation helpers

// SendValidationError sends a validation error with field details
func SendValidationError(c *gin.Context, message string, fieldErrors map[string]string) {
	c.JSON(http.StatusBadRequest, entities.ErrorResponse{
		Code:    ErrCodeValidationError,
		Message: message,
		Details: map[string]interface{}{
			"validation_errors": fieldErrors,
		},
	})
}

// SendInvalidField sends an error for a specific invalid field
func SendInvalidField(c *gin.Context, field, message string) {
	c.JSON(http.StatusBadRequest, entities.ErrorResponse{
		Code:    ErrCodeValidationError,
		Message: message,
		Details: map[string]interface{}{
			"field": field,
		},
	})
}
