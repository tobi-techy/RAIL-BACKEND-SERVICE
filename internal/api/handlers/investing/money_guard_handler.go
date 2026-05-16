package investing

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	"go.uber.org/zap"
)

type MoneyGuardHandler struct {
	service *moneyguardservice.Service
	logger  *zap.Logger
}

func NewMoneyGuardHandler(service *moneyguardservice.Service, logger *zap.Logger) *MoneyGuardHandler {
	return &MoneyGuardHandler{service: service, logger: logger}
}

func (h *MoneyGuardHandler) Summary(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	result, err := h.service.Summary(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("money guard summary failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to get money guard summary")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *MoneyGuardHandler) UpdateSettings(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	var req moneyguardservice.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	result, err := h.service.UpdateSettings(c.Request.Context(), userID, req)
	if err != nil {
		h.logger.Error("update settings failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to update settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *MoneyGuardHandler) CreateCap(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	var req moneyguardservice.CreateCapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	result, err := h.service.CreateCap(c.Request.Context(), userID, req)
	if err != nil {
		h.logger.Error("create cap failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to create spending cap")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": result})
}

func (h *MoneyGuardHandler) ListCaps(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	result, err := h.service.ListCaps(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to list caps")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *MoneyGuardHandler) DeleteCap(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "invalid cap id")
		return
	}
	if err := h.service.DeleteCap(c.Request.Context(), userID, id); err != nil {
		if err == sql.ErrNoRows {
			common.SendNotFound(c, common.ErrCodeNotFound, "cap not found")
			return
		}
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to delete cap")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *MoneyGuardHandler) WeeklyAutopsy(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	result, err := h.service.WeeklyAutopsy(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to get weekly autopsy")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *MoneyGuardHandler) RecoveryPlan(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	result, err := h.service.RecoveryPlan(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to get recovery plan")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}
