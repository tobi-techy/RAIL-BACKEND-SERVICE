package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.uber.org/zap"
)

type ComplianceHandler struct {
	auditService *services.AuditService
	logger       *zap.Logger
}

func NewComplianceHandler(auditService *services.AuditService, logger *zap.Logger) *ComplianceHandler {
	return &ComplianceHandler{
		auditService: auditService,
		logger:       logger,
	}
}

func (h *ComplianceHandler) RequestDataExport(c *gin.Context) {
	userID := uuid.MustParse(c.GetString("user_id"))

	exportURL, err := h.auditService.ProcessDataExport(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.Log(c.Request.Context(), &entities.AuditLog{
		UserID:    userID,
		Action:    entities.AuditActionDataExport,
		Resource:  "user_data",
		IPAddress: c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	})

	c.JSON(http.StatusOK, gin.H{"export_url": exportURL})
}

func (h *ComplianceHandler) RequestDataDeletion(c *gin.Context) {
	userID := uuid.MustParse(c.GetString("user_id"))

	if err := h.auditService.ProcessDataDeletion(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.Log(c.Request.Context(), &entities.AuditLog{
		UserID:    userID,
		Action:    entities.AuditActionDataDelete,
		Resource:  "user_data",
		IPAddress: c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	})

	c.JSON(http.StatusOK, gin.H{"message": "Data deletion request submitted"})
}
