package opportunities

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/opportunity"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type Handlers struct {
	svc    *opportunity.Service
	logger *zap.Logger
}

func NewHandlers(svc *opportunity.Service, logger *zap.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// GetRecommendations returns the user's top 3 weekly opportunities.
// GET /v1/opportunities/recommendations
func (h *Handlers) GetRecommendations(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	matches, err := h.svc.GetWeeklyRecommendations(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get recommendations", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get recommendations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recommendations": matches})
}

// GetListing returns a single opportunity listing.
// GET /v1/opportunities/:id
func (h *Handlers) GetListing(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid listing ID"})
		return
	}

	listing, err := h.svc.GetListing(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "listing not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"listing": listing})
}

// SaveOpportunity marks an opportunity as saved.
// POST /v1/opportunities/:id/save
func (h *Handlers) SaveOpportunity(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	listingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid listing ID"})
		return
	}

	if err := h.svc.SaveOpportunity(c.Request.Context(), userID, listingID); err != nil {
		h.logger.Error("Failed to save opportunity", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

// HideOpportunity marks an opportunity as hidden.
// POST /v1/opportunities/:id/hide
func (h *Handlers) HideOpportunity(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	listingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid listing ID"})
		return
	}

	if err := h.svc.HideOpportunity(c.Request.Context(), userID, listingID); err != nil {
		h.logger.Error("Failed to hide opportunity", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hide"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "hidden"})
}

// UpdateProfile creates or updates the user's opportunity profile.
// POST /v1/opportunities/profile
func (h *Handlers) UpdateProfile(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Skills            []string `json:"skills"`
		Interests         []string `json:"interests"`
		PreferredTypes    []string `json:"preferred_types"`
		HoursPerWeek      int      `json:"hours_per_week"`
		MinReward         float64  `json:"min_reward"`
		PreferredCurrency string   `json:"preferred_currency"`
		Bio               *string  `json:"bio"`
		PortfolioLinks    []string `json:"portfolio_links"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	profile := &entities.UserOpportunityProfile{
		UserID:            userID,
		Skills:            pq.StringArray(req.Skills),
		Interests:         pq.StringArray(req.Interests),
		PreferredTypes:    pq.StringArray(req.PreferredTypes),
		HoursPerWeek:      req.HoursPerWeek,
		MinReward:         decimal.NewFromFloat(req.MinReward),
		PreferredCurrency: req.PreferredCurrency,
		Bio:               req.Bio,
		PortfolioLinks:    pq.StringArray(req.PortfolioLinks),
	}
	if profile.PreferredCurrency == "" {
		profile.PreferredCurrency = "USDC"
	}
	if profile.HoursPerWeek <= 0 {
		profile.HoursPerWeek = 5
	}

	if err := h.svc.UpdateProfile(c.Request.Context(), profile); err != nil {
		h.logger.Error("Failed to update profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// GetProfile returns the user's opportunity profile.
// GET /v1/opportunities/profile
func (h *Handlers) GetProfile(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	profile, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		// Return empty profile if none exists
		c.JSON(http.StatusOK, gin.H{"profile": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{"profile": profile})
}

// TriggerSync manually triggers an opportunity sync (admin/internal).
// POST /internal/opportunities/sync
func (h *Handlers) TriggerSync(c *gin.Context) {
	go func() {
		if err := h.svc.SyncFromSuperteam(c.Request.Context()); err != nil {
			h.logger.Error("Manual sync failed", zap.Error(err))
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "sync started"})
}
