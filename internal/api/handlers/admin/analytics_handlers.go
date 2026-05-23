package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	analytics "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"go.uber.org/zap"
)

type AnalyticsHandlers struct {
	svc    *analytics.Service
	logger *zap.Logger
}

func NewAnalyticsHandlers(svc *analytics.Service, logger *zap.Logger) *AnalyticsHandlers {
	return &AnalyticsHandlers{svc: svc, logger: logger}
}

func (h *AnalyticsHandlers) Overview(c *gin.Context) {
	data, err := h.svc.GetOverview(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics overview failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load overview"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Users(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	data, err := h.svc.GetUsers(c.Request.Context(), limit, offset)
	if err != nil {
		h.logger.Error("analytics users failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load users"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Waitlist(c *gin.Context) {
	data, err := h.svc.GetWaitlist(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics waitlist failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load waitlist"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Miriam(c *gin.Context) {
	data, err := h.svc.GetMiriam(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics miriam failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load miriam"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) MoneyMovement(c *gin.Context) {
	data, err := h.svc.GetMoneyMovement(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics money-movement failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load money movement"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Retention(c *gin.Context) {
	data, err := h.svc.GetRetention(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics retention failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load retention"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Trust(c *gin.Context) {
	data, err := h.svc.GetTrust(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics trust failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load trust"})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *AnalyticsHandlers) Chains(c *gin.Context) {
	data, err := h.svc.GetChains(c.Request.Context())
	if err != nil {
		h.logger.Error("analytics chains failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load chains"})
		return
	}
	c.JSON(http.StatusOK, data)
}
