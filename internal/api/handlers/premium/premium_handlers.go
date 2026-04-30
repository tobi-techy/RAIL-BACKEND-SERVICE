package premium

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Handlers groups all premium feature HTTP handlers.
type Handlers struct {
	nairaShield     *premium.NairaShieldService
	blackTax        *premium.BlackTaxService
	receiptSplit    *premium.ReceiptSplitService
	scamIntel       *premium.ScamIntelligenceService
	taxResidency    *premium.TaxResidencyService
	incomeSmoothing *premium.IncomeSmoothingService
	financialTrauma *premium.FinancialTraumaService
	visaProof       *premium.VisaProofService
	panicButton     *premium.PanicButtonService
	logger          *zap.Logger
}

// NewHandlers creates all premium feature handlers.
func NewHandlers(
	nairaShield *premium.NairaShieldService,
	blackTax *premium.BlackTaxService,
	receiptSplit *premium.ReceiptSplitService,
	scamIntel *premium.ScamIntelligenceService,
	taxResidency *premium.TaxResidencyService,
	incomeSmoothing *premium.IncomeSmoothingService,
	financialTrauma *premium.FinancialTraumaService,
	visaProof *premium.VisaProofService,
	panicButton *premium.PanicButtonService,
	logger *zap.Logger,
) *Handlers {
	return &Handlers{
		nairaShield:     nairaShield,
		blackTax:        blackTax,
		receiptSplit:    receiptSplit,
		scamIntel:       scamIntel,
		taxResidency:    taxResidency,
		incomeSmoothing: incomeSmoothing,
		financialTrauma: financialTrauma,
		visaProof:       visaProof,
		panicButton:     panicButton,
		logger:          logger,
	}
}

// ===================== TIER 1: Naira Shield =====================

func (h *Handlers) GetNairaShield(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	report, err := h.nairaShield.GetShieldReport(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("naira shield report failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate shield report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

// ===================== TIER 1: Black Tax =====================

func (h *Handlers) GetBlackTaxSummary(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	summary, err := h.blackTax.GetSummary(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("black tax summary failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load family support summary"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

type setBudgetRequest struct {
	MonthlyLimit      string `json:"monthly_limit" binding:"required"`
	AlertThresholdPct int    `json:"alert_threshold_pct"`
}

func (h *Handlers) SetBlackTaxBudget(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req setBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	limit, err := decimal.NewFromString(req.MonthlyLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid monthly_limit"})
		return
	}

	if req.AlertThresholdPct <= 0 || req.AlertThresholdPct > 100 {
		req.AlertThresholdPct = 80
	}

	if err := h.blackTax.SetBudget(c.Request.Context(), userID, limit, req.AlertThresholdPct); err != nil {
		h.logger.Error("set black tax budget failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not set budget"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Budget updated"})
}

func (h *Handlers) SyncBlackTaxRecipients(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if err := h.blackTax.SyncRecipientsFromHistory(c.Request.Context(), userID); err != nil {
		h.logger.Error("sync recipients failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not sync recipients"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Recipients synced from transfer history"})
}

// ===================== TIER 1: Receipt Split =====================

type splitReceiptRequest struct {
	ReceiptID   uuid.UUID         `json:"receipt_id" binding:"required"`
	Assignments map[string]string `json:"assignments"` // item_name -> person
}

func (h *Handlers) SplitReceipt(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	_ = userID // may use for authorization checks

	var req splitReceiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	result, err := h.receiptSplit.CalculateSplit(c.Request.Context(), userID, req.ReceiptID, req.Assignments)
	if err != nil {
		h.logger.Error("receipt split failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not split receipt"})
		return
	}

	split, err := h.receiptSplit.PersistSplit(c.Request.Context(), userID, req.ReceiptID, result)
	if err != nil {
		h.logger.Error("persist receipt split failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save split"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": split})
}

// ===================== TIER 2: Scam Intelligence =====================

type checkMerchantRequest struct {
	MerchantName string `json:"merchant_name" binding:"required"`
}

func (h *Handlers) CheckMerchant(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req checkMerchantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	result, err := h.scamIntel.CheckMerchant(c.Request.Context(), userID, req.MerchantName)
	if err != nil {
		h.logger.Error("scam check failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not check merchant"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *Handlers) GetScamAlerts(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	alerts, err := h.scamIntel.GetActiveAlerts(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get scam alerts failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load alerts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": alerts})
}

func (h *Handlers) DismissScamAlert(c *gin.Context) {
	alertIDStr := c.Param("alertId")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert ID"})
		return
	}

	if err := h.scamIntel.DismissAlert(c.Request.Context(), alertID); err != nil {
		h.logger.Error("dismiss scam alert failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not dismiss alert"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Alert dismissed"})
}

// ===================== TIER 2: Tax Residency =====================

type logLocationRequest struct {
	Country string `json:"country" binding:"required"`
	Source  string `json:"source"` // manual, gps
}

func (h *Handlers) LogLocation(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req logLocationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	if req.Source == "" {
		req.Source = "manual"
	}

	if err := h.taxResidency.LogLocation(c.Request.Context(), userID, req.Country, req.Source); err != nil {
		h.logger.Error("log location failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not log location"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Location logged"})
}

func (h *Handlers) GetTaxResidency(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	status, err := h.taxResidency.GetResidencyStatus(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("tax residency check failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not check residency"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

type setTaxProfileRequest struct {
	PrimaryCountry   string `json:"primary_tax_country" binding:"required"`
	SecondaryCountry string `json:"secondary_tax_country"`
	AlertThreshold   int    `json:"alert_threshold"`
}

func (h *Handlers) SetTaxProfile(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req setTaxProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	var sec *string
	if req.SecondaryCountry != "" {
		sec = &req.SecondaryCountry
	}
	if req.AlertThreshold <= 0 {
		req.AlertThreshold = 150
	}

	if err := h.taxResidency.SetTaxProfile(c.Request.Context(), &entities.UserTaxProfile{
		UserID:              userID,
		PrimaryTaxCountry:   req.PrimaryCountry,
		SecondaryTaxCountry: sec,
		AlertThreshold:      req.AlertThreshold,
	}); err != nil {
		h.logger.Error("set tax profile failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Tax profile saved"})
}

// ===================== TIER 2: Income Smoothing =====================

func (h *Handlers) GetIncomeForecast(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	forecast, err := h.incomeSmoothing.GetForecast(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("income forecast failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate forecast"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": forecast})
}

// ===================== TIER 3: Financial Trauma =====================

func (h *Handlers) GetWellnessScore(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	score, err := h.financialTrauma.CalculateWellnessScore(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("wellness score failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not calculate wellness score"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": score})
}

// ===================== TIER 3: Visa Proof =====================

func (h *Handlers) GenerateVisaProof(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req premium.VisaProofPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	result, err := h.visaProof.GenerateProof(c.Request.Context(), userID, req)
	if err != nil {
		h.logger.Error("visa proof failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate proof"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *Handlers) GetVisaProofs(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	reqs, err := h.visaProof.GetRequests(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get visa proofs failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load visa proofs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": reqs})
}

// ===================== TIER 3: Panic Button =====================

type emergencyContactRequest struct {
	Name     string `json:"name" binding:"required"`
	Phone    string `json:"phone" binding:"required"`
	Relation string `json:"relation" binding:"required"`
	Priority int    `json:"priority"`
}

func (h *Handlers) GetEmergencyContacts(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	contacts, err := h.panicButton.GetContacts(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get contacts failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load contacts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": contacts})
}

func (h *Handlers) AddEmergencyContact(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req emergencyContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}

	if err := h.panicButton.AddContact(c.Request.Context(), userID, req.Name, req.Phone, req.Relation, req.Priority); err != nil {
		h.logger.Error("add contact failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not add contact"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Contact added"})
}

func (h *Handlers) RemoveEmergencyContact(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	contactIDStr := c.Param("contactId")
	contactID, err := uuid.Parse(contactIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	if err := h.panicButton.RemoveContact(c.Request.Context(), userID, contactID); err != nil {
		h.logger.Error("remove contact failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not remove contact"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Contact removed"})
}

type triggerLockRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) TriggerEmergencyLock(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req triggerLockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	if req.Reason == "" {
		req.Reason = "user_triggered"
	}

	lock, err := h.panicButton.TriggerLock(c.Request.Context(), userID, req.Reason)
	if err != nil {
		h.logger.Error("trigger lock failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not trigger lock. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": lock})
}

func (h *Handlers) GetEmergencyLock(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	lock, err := h.panicButton.GetActiveLock(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get active lock failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not check lock status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": lock})
}
