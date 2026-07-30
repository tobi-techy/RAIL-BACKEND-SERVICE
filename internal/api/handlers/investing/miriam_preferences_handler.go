package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"go.uber.org/zap"
)

// MiriamPreferencesHandler exposes GET/PUT /api/v1/ai/miriam/preferences.
type MiriamPreferencesHandler struct {
	svc    *miriamservice.PreferencesService
	logger *zap.Logger
}

func NewMiriamPreferencesHandler(svc *miriamservice.PreferencesService, logger *zap.Logger) *MiriamPreferencesHandler {
	return &MiriamPreferencesHandler{svc: svc, logger: logger}
}

func (h *MiriamPreferencesHandler) Get(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preferences unavailable"})
		return
	}
	prefs, err := h.svc.Get(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get miriam preferences", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load preferences"})
		return
	}
	tzSource := "default"
	if prefs.Timezone != nil && *prefs.Timezone != "" {
		tzSource = "user"
	} else {
		tzSource = "country"
	}
	c.JSON(http.StatusOK, gin.H{"data": miriamservice.APIView(prefs, tzSource)})
}

func (h *MiriamPreferencesHandler) Put(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preferences unavailable"})
		return
	}
	var req miriamservice.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prefs, err := h.svc.Update(c.Request.Context(), userID, req)
	if err != nil {
		h.logger.Warn("update miriam preferences", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tzSource := "default"
	if prefs.Timezone != nil && *prefs.Timezone != "" {
		tzSource = "user"
	} else {
		tzSource = "country"
	}
	c.JSON(http.StatusOK, gin.H{"data": miriamservice.APIView(prefs, tzSource)})
}
