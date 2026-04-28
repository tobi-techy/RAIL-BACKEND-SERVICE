package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/domain/services/subscription"
	"go.uber.org/zap"
)

// PremiumAIHandlers handles pro-gated AI feature endpoints.
type PremiumAIHandlers struct {
	orchestrator *aiservice.Orchestrator
	subService   *subscription.Service
	logger       *zap.Logger
}

func NewPremiumAIHandlers(orchestrator *aiservice.Orchestrator, subService *subscription.Service, logger *zap.Logger) *PremiumAIHandlers {
	return &PremiumAIHandlers{orchestrator: orchestrator, subService: subService, logger: logger}
}

func (h *PremiumAIHandlers) requirePro(c *gin.Context) bool {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	isPro, _ := h.subService.IsProUser(c.Request.Context(), userID)
	if !isPro {
		c.JSON(http.StatusForbidden, gin.H{"error": "pro_required", "message": "This feature requires Rail Pro"})
		return false
	}
	return true
}

// WeeklyReport generates a rich weekly financial report (pro-only).
func (h *PremiumAIHandlers) WeeklyReport(c *gin.Context) {
	if !h.requirePro(c) {
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID,
		"Generate my detailed weekly financial report. Include: total spent this week vs last week, spending by category breakdown, stash growth, yield earned, and one actionable recommendation. Format with clear sections.",
		nil)
	if err != nil {
		h.logger.Error("weekly report failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"content":     resp.Content,
			"cards":       resp.Cards,
			"tokens_used": resp.TokensUsed,
		},
	})
}

// Simulate runs a what-if savings projection (pro-only).
func (h *PremiumAIHandlers) Simulate(c *gin.Context) {
	if !h.requirePro(c) {
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	var req struct {
		DepositAmount    float64 `json:"deposit_amount" binding:"required,gt=0"`
		DepositFrequency string  `json:"deposit_frequency" binding:"required,oneof=weekly monthly"`
		DurationMonths   int     `json:"duration_months" binding:"required,gt=0,lte=120"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, infraai.ToolCall{
		ID:   "simulate-http",
		Name: aiservice.ToolSimulateSavings,
		Arguments: map[string]interface{}{
			"deposit_amount":    req.DepositAmount,
			"deposit_frequency": req.DepositFrequency,
			"duration_months":   float64(req.DurationMonths),
		},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "simulation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// TaxSummary generates a tax report (pro-only).
func (h *PremiumAIHandlers) TaxSummary(c *gin.Context) {
	if !h.requirePro(c) {
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	year := c.DefaultQuery("year", "2026")
	resp, err := h.orchestrator.Chat(c.Request.Context(), userID,
		"Generate my tax summary for "+year+". Include all deposits, yield earned, stablecoin transactions, and any capital gains. Format as a clear report I can share with my accountant.",
		nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate tax summary"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"content":     resp.Content,
			"cards":       resp.Cards,
			"tokens_used": resp.TokensUsed,
			"year":        year,
		},
	})
}

// GenerateChallenge creates a personalized spending challenge (pro-only).
func (h *PremiumAIHandlers) GenerateChallenge(c *gin.Context) {
	if !h.requirePro(c) {
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID,
		"Based on my spending patterns, create one personalized money challenge for this week. Make it specific, achievable, and fun. Include: challenge name, description, target amount to save, and duration. Format as a structured response.",
		nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"content":     resp.Content,
			"cards":       resp.Cards,
			"tokens_used": resp.TokensUsed,
		},
	})
}

// GoalProgress returns goal tracking with catch-up suggestions (pro-only).
func (h *PremiumAIHandlers) GoalProgress(c *gin.Context) {
	if !h.requirePro(c) {
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID,
		"Check my savings goals progress. For each goal: show current amount vs target, percentage complete, whether I'm on track, and if behind suggest a specific catch-up amount to transfer this week. Be encouraging.",
		nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get goal progress"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"content":        resp.Content,
			"cards":          resp.Cards,
			"pending_action": resp.PendingAction,
			"tokens_used":    resp.TokensUsed,
		},
	})
}
