package investing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// PremiumAIHandlers handles AI feature endpoints.
type PremiumAIHandlers struct {
	orchestrator      *aiservice.Orchestrator
	convService       *conversationsvc.Service
	passcodeValidator automationPasscodeValidator
	logger            *zap.Logger
}

func NewPremiumAIHandlers(orchestrator *aiservice.Orchestrator, logger *zap.Logger, convService ...*conversationsvc.Service) *PremiumAIHandlers {
	h := &PremiumAIHandlers{orchestrator: orchestrator, logger: logger}
	if len(convService) > 0 {
		h.convService = convService[0]
	}
	return h
}

func (h *PremiumAIHandlers) SetPasscodeValidator(validator automationPasscodeValidator) {
	h.passcodeValidator = validator
}

// WeeklyReport generates a rich weekly financial report.
func (h *PremiumAIHandlers) WeeklyReport(c *gin.Context) {
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

// Simulate runs a what-if savings projection.
func (h *PremiumAIHandlers) Simulate(c *gin.Context) {
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

// TaxSummary generates a tax report.
func (h *PremiumAIHandlers) TaxSummary(c *gin.Context) {
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

// OperatingPlan returns Miriam's persona-aware monthly operating plan.
func (h *PremiumAIHandlers) OperatingPlan(c *gin.Context) {
	userID, _ := common.GetUserIDFromContext(c)

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, infraai.ToolCall{
		ID:        "operating-plan-http",
		Name:      aiservice.ToolGetMoneyOperatingPlan,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("operating plan failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate operating plan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// StageOperatingPlanAction stages a plan proposal into the pending-action confirmation flow.
func (h *PremiumAIHandlers) StageOperatingPlanAction(c *gin.Context) {
	if h.convService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation service unavailable"})
		return
	}
	userID, _ := common.GetUserIDFromContext(c)

	var req struct {
		ConversationID string                 `json:"conversation_id"`
		Type           string                 `json:"type" binding:"required"`
		Params         map[string]interface{} `json:"params" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Type == "create_automation" && isTransferAutomation(valueString(req.Params["action_type"])) {
		actionConfig, _ := req.Params["action_config"].(map[string]interface{})
		if actionConfig == nil {
			actionConfig = map[string]interface{}{}
			req.Params["action_config"] = actionConfig
		}
		if !h.requirePlanTransferAutomationPasscode(c, userID, actionConfig) {
			return
		}
	}

	conv, err := h.getOrCreatePlanConversation(c.Request.Context(), userID, req.ConversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.orchestrator.StageOperatingPlanAction(c.Request.Context(), userID, conv.ID, req.Type, req.Params)
	if err != nil {
		h.logger.Error("stage operating plan action failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stage action"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"conversation_id": conv.ID,
		"staged":          result,
		"confirm_url":     "/api/v1/ai/conversations/" + conv.ID.String() + "/confirm",
		"cancel_url":      "/api/v1/ai/conversations/" + conv.ID.String() + "/cancel",
	}})
}

func (h *PremiumAIHandlers) getOrCreatePlanConversation(ctx context.Context, userID uuid.UUID, conversationID string) (*entities.AIConversation, error) {
	if conversationID == "" {
		return h.convService.CreateConversation(ctx, userID, "Miriam operating plan")
	}
	convID, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, err
	}
	conv, err := h.convService.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.UserID != userID {
		return nil, fmt.Errorf("conversation not found")
	}
	return conv, nil
}

func (h *PremiumAIHandlers) requirePlanTransferAutomationPasscode(c *gin.Context, userID uuid.UUID, actionConfig map[string]interface{}) bool {
	if h.passcodeValidator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "PASSCODE_SESSION_UNAVAILABLE", "message": "Passcode session validation is currently unavailable"})
		return false
	}
	token := strings.TrimSpace(c.GetHeader("X-Passcode-Session"))
	if token == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PASSCODE_SESSION_REQUIRED", "message": "Passcode verification is required to create a transfer automation"})
		return false
	}
	valid, err := h.passcodeValidator.ValidateSession(c.Request.Context(), userID, token)
	if err != nil || !valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "PASSCODE_SESSION_INVALID", "message": "Passcode session is invalid or expired"})
		return false
	}
	automation.StampTransferConsent(actionConfig, time.Now().UTC())
	if err := h.passcodeValidator.InvalidateSession(c.Request.Context(), userID, token); err != nil {
		h.logger.Warn("failed to invalidate passcode session after operating plan automation consent", zap.Error(err), zap.String("user_id", userID.String()))
	}
	return true
}

func valueString(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// MoneyAcrossBordersReport returns a geography and currency report for diaspora/cross-border users.
func (h *PremiumAIHandlers) MoneyAcrossBordersReport(c *gin.Context) {
	userID, _ := common.GetUserIDFromContext(c)

	persona, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, infraai.ToolCall{
		ID:        "persona-context-http",
		Name:      aiservice.ToolGetPersonaMoneyContext,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("money across borders persona context failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate report"})
		return
	}
	plan, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, infraai.ToolCall{
		ID:        "money-across-borders-plan-http",
		Name:      aiservice.ToolGetMoneyOperatingPlan,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("money across borders plan failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"persona_context":          persona,
			"operating_plan":           plan,
			"professional_review_note": "Cross-border tax, legal, estate, and investment notes are informational. Review with a qualified professional before filing, investing, or changing legal documents.",
		},
	})
}

// GenerateChallenge creates a personalized spending challenge.
func (h *PremiumAIHandlers) GenerateChallenge(c *gin.Context) {
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

// GoalProgress returns goal tracking with catch-up suggestions.
func (h *PremiumAIHandlers) GoalProgress(c *gin.Context) {
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
