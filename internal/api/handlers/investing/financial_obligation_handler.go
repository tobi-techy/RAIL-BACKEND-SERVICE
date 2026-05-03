package investing

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	"go.uber.org/zap"
)

// FinancialObligationHandler exposes manual obligations for Miriam planning.
type FinancialObligationHandler struct {
	service *obligationservice.Service
	logger  *zap.Logger
}

func NewFinancialObligationHandler(service *obligationservice.Service, logger *zap.Logger) *FinancialObligationHandler {
	return &FinancialObligationHandler{service: service, logger: logger}
}

func (h *FinancialObligationHandler) Create(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	var req obligationservice.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	result, err := h.service.Create(c.Request.Context(), userID, req)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeValidationError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": result})
}

func (h *FinancialObligationHandler) List(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	result, err := h.service.List(c.Request.Context(), userID, obligationservice.ListFilter{
		Status: c.Query("status"),
		Type:   c.Query("type"),
	})
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeValidationError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *FinancialObligationHandler) Get(c *gin.Context) {
	userID, id, ok := obligationIDs(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), userID, id)
	if err != nil {
		if err == sql.ErrNoRows {
			common.SendNotFound(c, common.ErrCodeNotFound, "Financial obligation not found")
			return
		}
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *FinancialObligationHandler) Update(c *gin.Context) {
	userID, id, ok := obligationIDs(c)
	if !ok {
		return
	}

	var req obligationservice.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	result, err := h.service.Update(c.Request.Context(), userID, id, req)
	if err != nil {
		if err == sql.ErrNoRows {
			common.SendNotFound(c, common.ErrCodeNotFound, "Financial obligation not found")
			return
		}
		common.SendBadRequest(c, common.ErrCodeValidationError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *FinancialObligationHandler) Delete(c *gin.Context) {
	userID, id, ok := obligationIDs(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), userID, id); err != nil {
		if err == sql.ErrNoRows {
			common.SendNotFound(c, common.ErrCodeNotFound, "Financial obligation not found")
			return
		}
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func obligationIDs(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid financial obligation ID")
		return uuid.Nil, uuid.Nil, false
	}
	return userID, id, true
}
