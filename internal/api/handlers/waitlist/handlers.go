package waitlist

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
	waitlistsvc "github.com/rail-service/rail_service/internal/domain/services/waitlist"
	"go.uber.org/zap"
)

type Handlers struct {
	svc    *waitlistsvc.Service
	logger *zap.Logger
}

func NewHandlers(svc *waitlistsvc.Service, logger *zap.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// Signup handles POST /api/v1/waitlist (public, no auth)
func (h *Handlers) Signup(c *gin.Context) {
	var req waitlistsvc.SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := h.svc.Signup(c.Request.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		h.logger.Error("waitlist signup failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "signup failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"position":      resp.Position,
		"referral_code": resp.ReferralCode,
		"total_ahead":   resp.TotalAhead,
	})
}

// List handles GET /api/v1/admin/waitlist (admin only)
func (h *Handlers) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var status *entities.WaitlistStatus
	if s := c.Query("status"); s != "" {
		st := entities.WaitlistStatus(s)
		status = &st
	}

	users, total, err := h.svc.List(c.Request.Context(), status, limit, offset)
	if err != nil {
		h.logger.Error("waitlist list failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"total": total,
		"limit": limit,
		"offset": offset,
	})
}

// Count handles GET /api/v1/waitlist/count (public)
func (h *Handlers) Count(c *gin.Context) {
	count, err := h.svc.Count(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "count failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}
