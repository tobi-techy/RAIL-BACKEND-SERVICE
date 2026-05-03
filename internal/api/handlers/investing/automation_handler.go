package investing

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
)

type automationPasscodeValidator interface {
	ValidateSession(ctx context.Context, userID uuid.UUID, token string) (bool, error)
	InvalidateSession(ctx context.Context, userID uuid.UUID, token string) error
}

// AutomationHandler handles CRUD for Miriam automations.
type AutomationHandler struct {
	service           *automation.Service
	passcodeValidator automationPasscodeValidator
	logger            *zap.Logger
}

func NewAutomationHandler(service *automation.Service, logger *zap.Logger, validator ...automationPasscodeValidator) *AutomationHandler {
	h := &AutomationHandler{service: service, logger: logger}
	if len(validator) > 0 {
		h.passcodeValidator = validator[0]
	}
	return h
}

// CreateAutomation handles POST /v1/ai/automations
func (h *AutomationHandler) CreateAutomation(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	var req automation.CreateAutomationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	if isTransferAutomation(req.ActionType) {
		if !h.requireTransferAutomationPasscode(c, userID, req.ActionConfig) {
			return
		}
	}

	result, err := h.service.Create(c.Request.Context(), userID, &req)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": result})
}

// ListAutomations handles GET /v1/ai/automations
func (h *AutomationHandler) ListAutomations(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	list, err := h.service.List(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": list})
}

// GetAutomation handles GET /v1/ai/automations/:id
func (h *AutomationHandler) GetAutomation(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid automation ID")
		return
	}

	result, err := h.service.Get(c.Request.Context(), userID, id)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Automation not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// UpdateAutomation handles PATCH /v1/ai/automations/:id
func (h *AutomationHandler) UpdateAutomation(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid automation ID")
		return
	}

	var req automation.UpdateAutomationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	existing, err := h.service.Get(c.Request.Context(), userID, id)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Automation not found")
		return
	}
	if isTransferAutomation(existing.ActionType) {
		req.ActionConfig = mergeAutomationActionConfig(existing.ActionConfig, req.ActionConfig)
		if !h.requireTransferAutomationPasscode(c, userID, req.ActionConfig) {
			return
		}
	}

	result, err := h.service.Update(c.Request.Context(), userID, id, &req)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

func mergeAutomationActionConfig(existingRaw json.RawMessage, update map[string]interface{}) map[string]interface{} {
	merged := map[string]interface{}{}
	_ = json.Unmarshal(existingRaw, &merged)
	for key, value := range update {
		merged[key] = value
	}
	return merged
}

// DeleteAutomation handles DELETE /v1/ai/automations/:id
func (h *AutomationHandler) DeleteAutomation(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid automation ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), userID, id); err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// GetAutomationLogs handles GET /v1/ai/automations/logs
func (h *AutomationHandler) GetAutomationLogs(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	logs, err := h.service.GetLogs(c.Request.Context(), userID, 50)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": logs})
}

func isTransferAutomation(actionType string) bool {
	return actionType == "transfer_to_stash" || actionType == "transfer_to_spend"
}

func (h *AutomationHandler) requireTransferAutomationPasscode(c *gin.Context, userID uuid.UUID, actionConfig map[string]interface{}) bool {
	if h.passcodeValidator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "PASSCODE_SESSION_UNAVAILABLE", "message": "Passcode session validation is currently unavailable"})
		return false
	}
	token := strings.TrimSpace(c.GetHeader("X-Passcode-Session"))
	if token == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PASSCODE_SESSION_REQUIRED", "message": "Passcode verification is required to create or update a transfer automation"})
		return false
	}
	valid, err := h.passcodeValidator.ValidateSession(c.Request.Context(), userID, token)
	if err != nil || !valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "PASSCODE_SESSION_INVALID", "message": "Passcode session is invalid or expired"})
		return false
	}
	automation.StampTransferConsent(actionConfig, time.Now().UTC())
	if err := h.passcodeValidator.InvalidateSession(c.Request.Context(), userID, token); err != nil {
		h.logger.Warn("failed to invalidate passcode session after transfer automation consent", zap.Error(err), zap.String("user_id", userID.String()))
	}
	return true
}
