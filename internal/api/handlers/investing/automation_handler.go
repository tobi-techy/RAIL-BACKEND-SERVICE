package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
)

// AutomationHandler handles CRUD for Miriam automations.
type AutomationHandler struct {
	service *automation.Service
	logger  *zap.Logger
}

func NewAutomationHandler(service *automation.Service, logger *zap.Logger) *AutomationHandler {
	return &AutomationHandler{service: service, logger: logger}
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

	result, err := h.service.Update(c.Request.Context(), userID, id, &req)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
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
