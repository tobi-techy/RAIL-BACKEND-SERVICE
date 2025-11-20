package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services/onboarding"
)

// OnboardingHandlers contains the onboarding-related HTTP handlers
type OnboardingHandlers struct {
	onboardingService *onboarding.Service
	validator         *validator.Validate
	logger            *zap.Logger
}

// NewOnboardingHandlers creates a new instance of onboarding handlers
func NewOnboardingHandlers(onboardingService *onboarding.Service, logger *zap.Logger) *OnboardingHandlers {
	return &OnboardingHandlers{
		onboardingService: onboardingService,
		validator:         validator.New(),
		logger:            logger,
	}
}

// StartOnboarding handles POST /onboarding/start
// @Summary Start user onboarding
// @Description Initiates the onboarding process for a new user with email/phone verification
// @Tags onboarding
// @Accept json
// @Produce json
// @Param request body entities.OnboardingStartRequest true "Onboarding start data"
// @Success 201 {object} entities.OnboardingStartResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 409 {object} entities.ErrorResponse "User already exists"
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/onboarding/start [post]
func (h *OnboardingHandlers) StartOnboarding(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Starting onboarding process",
		zap.String("request_id", getRequestID(c)),
		zap.String("ip", c.ClientIP()))

	var req entities.OnboardingStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Process onboarding start
	response, err := h.onboardingService.StartOnboarding(ctx, &req)
	if err != nil {
		h.logger.Error("Failed to start onboarding",
			zap.Error(err),
			zap.String("email", req.Email))

		// Check for specific error types
		if isUserAlreadyExistsError(err) {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: "User already exists with this email",
				Details: map[string]interface{}{"email": req.Email},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ONBOARDING_FAILED",
			Message: "Failed to start onboarding process",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("Onboarding started successfully",
		zap.String("user_id", response.UserID.String()),
		zap.String("email", req.Email))

	c.JSON(http.StatusCreated, response)
}

// GetOnboardingStatus handles GET /onboarding/status
// @Summary Get onboarding status
// @Description Returns the current onboarding status for the authenticated user
// @Tags onboarding
// @Produce json
// @Param user_id query string false "User ID (for admin use)"
// @Success 200 {object} entities.OnboardingStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "User not found"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/status [get]
func (h *OnboardingHandlers) GetOnboardingStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context or query parameter
	userID, err := h.getUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	h.logger.Debug("Getting onboarding status",
		zap.String("user_id", userID.String()),
		zap.String("request_id", getRequestID(c)))

	// Get onboarding status
	response, err := h.onboardingService.GetOnboardingStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get onboarding status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		// Handle inactive account explicitly
		if strings.Contains(strings.ToLower(err.Error()), "inactive") {
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "USER_INACTIVE",
				Message: "User account is inactive",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		if isUserNotFoundError(err) {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "USER_NOT_FOUND",
				Message: "User not found",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "STATUS_RETRIEVAL_FAILED",
			Message: "Failed to retrieve onboarding status",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Debug("Retrieved onboarding status successfully",
		zap.String("user_id", userID.String()),
		zap.String("status", string(response.OnboardingStatus)))

	c.JSON(http.StatusOK, response)
}

// GetKYCStatus handles GET /kyc/status
// @Summary Get KYC status
// @Description Returns the user's current KYC verification status and guidance
// @Tags onboarding
// @Produce json
// @Success 200 {object} entities.KYCStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/kyc/status [get]
func (h *OnboardingHandlers) GetKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := h.getUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID for KYC status", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	status, err := h.onboardingService.GetKYCStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get KYC status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_STATUS_ERROR",
			Message: "Failed to retrieve KYC status",
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// SubmitKYC handles POST /onboarding/kyc/submit
// @Summary Submit KYC documents
// @Description Submits KYC documents for verification
// @Tags onboarding
// @Accept json
// @Produce json
// @Param request body entities.KYCSubmitRequest true "KYC submission data"
// @Success 202 {object} map[string]interface{} "KYC submission accepted"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 403 {object} entities.ErrorResponse "User not eligible for KYC"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/kyc/submit [post]
func (h *OnboardingHandlers) SubmitKYC(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := h.getUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	h.logger.Info("Submitting KYC documents",
		zap.String("user_id", userID.String()),
		zap.String("request_id", getRequestID(c)))

	var req entities.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid KYC request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid KYC request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("KYC request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "KYC request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Submit KYC
	err = h.onboardingService.SubmitKYC(ctx, userID, &req)
	if err != nil {
		h.logger.Error("Failed to submit KYC",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if isKYCNotEligibleError(err) {
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "KYC_NOT_ELIGIBLE",
				Message: "User is not eligible for KYC submission",
				Details: map[string]interface{}{"error": err.Error()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_SUBMISSION_FAILED",
			Message: "Failed to submit KYC documents",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("KYC submitted successfully",
		zap.String("user_id", userID.String()))

	c.JSON(http.StatusAccepted, gin.H{
		"message": "KYC documents submitted successfully",
		"status":  "processing",
		"user_id": userID.String(),
		"next_steps": []string{
			"Wait for KYC review",
			"You can continue using core features while verification completes",
			"KYC unlocks virtual accounts, cards, and fiat withdrawals",
		},
	})
}

// ProcessKYCCallback handles KYC provider callbacks
// @Summary Process KYC callback
// @Description Handles callbacks from KYC providers with verification results
// @Tags onboarding
// @Accept json
// @Produce json
// @Param provider_ref path string true "KYC provider reference"
// @Param request body map[string]interface{} true "KYC callback data"
// @Success 200 {object} map[string]interface{} "Callback processed"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/onboarding/kyc/callback/{provider_ref} [post]
func (h *OnboardingHandlers) ProcessKYCCallback(c *gin.Context) {
	ctx := c.Request.Context()

	providerRef := c.Param("provider_ref")
	if providerRef == "" {
		h.logger.Warn("Missing provider reference in KYC callback")
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "MISSING_PROVIDER_REF",
			Message: "Provider reference is required",
		})
		return
	}

	h.logger.Info("Processing KYC callback",
		zap.String("provider_ref", providerRef),
		zap.String("request_id", getRequestID(c)))

	var callbackData map[string]interface{}
	if err := c.ShouldBindJSON(&callbackData); err != nil {
		h.logger.Warn("Invalid KYC callback payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_CALLBACK",
			Message: "Invalid callback payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Extract status and rejection reasons from callback
	// This would depend on the specific KYC provider's callback format
	status := entities.KYCStatusProcessing
	var rejectionReasons []string

	var reviewResult map[string]interface{}
	if raw, ok := callbackData["reviewResult"]; ok {
		if rr, ok := raw.(map[string]interface{}); ok {
			reviewResult = rr
		}
	}
	if reviewResult == nil {
		if payloadRaw, ok := callbackData["payload"].(map[string]interface{}); ok {
			if rr, ok := payloadRaw["reviewResult"].(map[string]interface{}); ok {
				reviewResult = rr
			}
		}
	}

	if reviewResult != nil {
		if answer, ok := reviewResult["reviewAnswer"].(string); ok {
			switch strings.ToUpper(strings.TrimSpace(answer)) {
			case "GREEN":
				status = entities.KYCStatusApproved
			case "RED":
				status = entities.KYCStatusRejected
			}
		}
		if labels, ok := reviewResult["rejectLabels"].([]interface{}); ok {
			for _, label := range labels {
				switch v := label.(type) {
				case map[string]interface{}:
					if desc, ok := v["description"].(string); ok && desc != "" {
						rejectionReasons = append(rejectionReasons, desc)
					} else if code, ok := v["code"].(string); ok && code != "" {
						rejectionReasons = append(rejectionReasons, code)
					}
				case string:
					if strings.TrimSpace(v) != "" {
						rejectionReasons = append(rejectionReasons, strings.TrimSpace(v))
					}
				}
			}
		}
	}

	if status == entities.KYCStatusProcessing {
		if statusStr, ok := callbackData["status"].(string); ok {
			switch strings.ToLower(statusStr) {
			case "approved", "passed":
				status = entities.KYCStatusApproved
			case "rejected", "failed":
				status = entities.KYCStatusRejected
				if reasons, ok := callbackData["rejection_reasons"].([]interface{}); ok {
					for _, reason := range reasons {
						if reasonStr, ok := reason.(string); ok {
							rejectionReasons = append(rejectionReasons, reasonStr)
						}
					}
				}
			case "processing", "pending":
				status = entities.KYCStatusProcessing
			}
		}
	}

	// Process the callback
	err := h.onboardingService.ProcessKYCCallback(ctx, providerRef, status, rejectionReasons)
	if err != nil {
		h.logger.Error("Failed to process KYC callback",
			zap.Error(err),
			zap.String("provider_ref", providerRef),
			zap.String("status", string(status)))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "CALLBACK_PROCESSING_FAILED",
			Message: "Failed to process KYC callback",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("KYC callback processed successfully",
		zap.String("provider_ref", providerRef),
		zap.String("status", string(status)))

	c.JSON(http.StatusOK, gin.H{
		"message":      "Callback processed successfully",
		"provider_ref": providerRef,
		"status":       string(status),
	})
}

// Helper methods

func (h *OnboardingHandlers) getUserID(c *gin.Context) (uuid.UUID, error) {
	// Try to get from authenticated user context first
	if userIDStr, exists := c.Get("user_id"); exists {
		if userID, ok := userIDStr.(uuid.UUID); ok {
			return userID, nil
		}
		if userIDStr, ok := userIDStr.(string); ok {
			return uuid.Parse(userIDStr)
		}
	}

	// Fallback to query parameter for development/admin use
	userIDQuery := c.Query("user_id")
	if userIDQuery != "" {
		return uuid.Parse(userIDQuery)
	}

	return uuid.Nil, fmt.Errorf("user ID not found in context or query parameters")
}

// Error type checking functions
func isUserAlreadyExistsError(err error) bool {
	// Implementation would check for specific error types
	// For now, check error message
	return err != nil && (contains(err.Error(), "user already exists") ||
		contains(err.Error(), "duplicate") ||
		contains(err.Error(), "conflict"))
}

func isKYCNotEligibleError(err error) bool {
	return err != nil && (contains(err.Error(), "cannot start KYC") ||
		contains(err.Error(), "not eligible"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) &&
			(s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				containsSubstring(s, substr))))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
