package investing

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	alpacaService "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	"github.com/rail-service/rail_service/pkg/logger"
)

// InvestmentHandlers handles investment-related API endpoints
type InvestmentHandlers struct {
	accountService   *alpacaService.AccountService
	fundingBridge    *alpacaService.FundingBridge
	portfolioSync    *alpacaService.PortfolioSyncService
	logger           *logger.Logger
}

func NewInvestmentHandlers(
	accountService *alpacaService.AccountService,
	fundingBridge *alpacaService.FundingBridge,
	portfolioSync *alpacaService.PortfolioSyncService,
	logger *logger.Logger,
) *InvestmentHandlers {
	return &InvestmentHandlers{
		accountService: accountService,
		fundingBridge:  fundingBridge,
		portfolioSync:  portfolioSync,
		logger:         logger,
	}
}

// GetBrokerageAccount returns the user's Alpaca brokerage account
// GET /api/v1/investment/account
func (h *InvestmentHandlers) GetBrokerageAccount(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	account, err := h.accountService.GetUserAccount(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get brokerage account", "error", err, "user_id", userID.String())
		common.RespondInternalError(c, "Failed to get account")
		return
	}

	if account == nil {
		c.JSON(http.StatusOK, gin.H{
			"has_account": false,
			"message":     "No brokerage account found. Create one to start investing.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"has_account":    true,
		"account_id":     account.ID,
		"account_number": account.AlpacaAccountNumber,
		"status":         account.Status,
		"buying_power":   account.BuyingPower.String(),
		"cash":           account.Cash.String(),
		"portfolio_value": account.PortfolioValue.String(),
		"trading_blocked": account.TradingBlocked,
		"last_synced_at": account.LastSyncedAt,
	})
}

// CreateBrokerageAccount creates an Alpaca brokerage account for the user
// POST /api/v1/investment/account
func (h *InvestmentHandlers) CreateBrokerageAccount(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	alpacaResp, err := h.accountService.CreateAccountForUser(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to create brokerage account", "error", err, "user_id", userID.String())
		common.RespondError(c, http.StatusBadRequest, "ACCOUNT_CREATION_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":        "Brokerage account created successfully",
		"account_id":     alpacaResp.ID,
		"account_number": alpacaResp.AccountNumber,
		"status":         alpacaResp.Status,
	})
}

// FundBrokerageAccount transfers funds from Circle wallet to Alpaca buying power
// POST /api/v1/investment/fund
func (h *InvestmentHandlers) FundBrokerageAccount(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req struct {
		Amount string `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request", map[string]interface{}{"error": err.Error()})
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		common.RespondBadRequest(c, "Invalid amount", nil)
		return
	}

	if err := h.fundingBridge.TransferFromCircleToAlpaca(c.Request.Context(), userID, amount); err != nil {
		h.logger.Error("Failed to fund brokerage account", "error", err, "user_id", userID.String())
		common.RespondError(c, http.StatusBadRequest, "FUNDING_FAILED", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Funding initiated successfully",
		"amount":  amount.String(),
	})
}

// GetFundingLimits returns instant funding limits
// GET /api/v1/investment/funding/limits
func (h *InvestmentHandlers) GetFundingLimits(c *gin.Context) {
	limits, err := h.fundingBridge.GetInstantFundingLimits(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get funding limits", "error", err)
		common.RespondInternalError(c, "Failed to get funding limits")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"amount_available": limits.AmountAvailable.String(),
		"amount_in_use":    limits.AmountInUse.String(),
		"amount_limit":     limits.AmountLimit.String(),
	})
}

// GetPendingFunding returns pending instant funding transfers
// GET /api/v1/investment/funding/pending
func (h *InvestmentHandlers) GetPendingFunding(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	pending, err := h.fundingBridge.GetPendingFunding(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get pending funding", "error", err, "user_id", userID.String())
		common.RespondInternalError(c, "Failed to get pending funding")
		return
	}

	c.JSON(http.StatusOK, gin.H{"pending_transfers": pending})
}

// SyncPositions synchronizes positions with Alpaca
// POST /api/v1/investment/positions/sync
func (h *InvestmentHandlers) SyncPositions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	if err := h.portfolioSync.SyncPositions(c.Request.Context(), userID); err != nil {
		h.logger.Error("Failed to sync positions", "error", err, "user_id", userID.String())
		common.RespondInternalError(c, "Failed to sync positions")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Positions synced successfully"})
}

// GetPositions returns user's investment positions
// GET /api/v1/investment/positions
func (h *InvestmentHandlers) GetPositions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	// Sync first if requested
	if c.Query("sync") == "true" {
		if err := h.portfolioSync.SyncPositions(c.Request.Context(), userID); err != nil {
			h.logger.Warn("Failed to sync positions", "error", err)
		}
	}

	summary, err := h.portfolioSync.GetPortfolioSummary(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get positions", "error", err, "user_id", userID.String())
		common.RespondInternalError(c, "Failed to get positions")
		return
	}

	if summary == nil {
		c.JSON(http.StatusOK, gin.H{
			"positions":      []interface{}{},
			"total_value":    "0",
			"buying_power":   "0",
			"unrealized_pl":  "0",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"buying_power":     summary.BuyingPower.String(),
		"cash":             summary.Cash.String(),
		"portfolio_value":  summary.PortfolioValue.String(),
		"equity":           summary.Equity.String(),
		"market_value":     summary.MarketValue.String(),
		"unrealized_pl":    summary.UnrealizedPL.String(),
		"cost_basis":       summary.CostBasis.String(),
		"position_count":   summary.PositionCount,
		"trading_blocked":  summary.TradingBlocked,
	})
}

// ReconcilePortfolio reconciles local and Alpaca portfolio data
// POST /api/v1/investment/reconcile
func (h *InvestmentHandlers) ReconcilePortfolio(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	report, err := h.portfolioSync.ReconcilePortfolio(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to reconcile portfolio", "error", err, "user_id", userID.String())
		common.RespondInternalError(c, "Failed to reconcile portfolio")
		return
	}

	c.JSON(http.StatusOK, report)
}

// GetBuyingPower returns the user's current buying power
// GET /api/v1/investment/buying-power
func (h *InvestmentHandlers) GetBuyingPower(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	buyingPower, err := h.accountService.GetBuyingPower(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get buying power", "error", err, "user_id", userID.String())
		c.JSON(http.StatusOK, gin.H{"buying_power": "0", "has_account": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"buying_power": buyingPower.String(),
		"has_account":  true,
	})
}

// PlaceOrder places a trade order via Alpaca
// POST /api/v1/investment/orders
func (h *InvestmentHandlers) PlaceOrder(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req struct {
		Symbol      string  `json:"symbol" binding:"required"`
		Side        string  `json:"side" binding:"required,oneof=buy sell"`
		Type        string  `json:"type" binding:"required,oneof=market limit stop stop_limit"`
		TimeInForce string  `json:"time_in_force" binding:"required,oneof=day gtc ioc fok"`
		Qty         *string `json:"qty"`
		Notional    *string `json:"notional"`
		LimitPrice  *string `json:"limit_price"`
		StopPrice   *string `json:"stop_price"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request", map[string]interface{}{"error": err.Error()})
		return
	}

	// Validate qty or notional is provided
	if req.Qty == nil && req.Notional == nil {
		common.RespondBadRequest(c, "Either qty or notional must be provided", nil)
		return
	}

	// Get user's Alpaca account
	account, err := h.accountService.GetUserAccount(c.Request.Context(), userID)
	if err != nil || account == nil {
		common.RespondError(c, http.StatusBadRequest, "NO_ACCOUNT", "No brokerage account found", nil)
		return
	}

	if account.TradingBlocked {
		common.RespondError(c, http.StatusForbidden, "TRADING_BLOCKED", "Trading is blocked on this account", nil)
		return
	}

	// For now, return a placeholder - actual order placement would go through Alpaca client
	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Order placement not yet implemented",
		"symbol":     req.Symbol,
		"side":       req.Side,
		"account_id": account.AlpacaAccountID,
	})
}

// GetOrders returns user's orders
// GET /api/v1/investment/orders
func (h *InvestmentHandlers) GetOrders(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	// Placeholder - would fetch from order repository
	_ = userID
	_ = limit
	_ = offset

	c.JSON(http.StatusOK, gin.H{
		"orders": []interface{}{},
		"total":  0,
	})
}

// GetOrder returns a specific order
// GET /api/v1/investment/orders/:id
func (h *InvestmentHandlers) GetOrder(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	orderID := c.Param("id")
	if orderID == "" {
		common.RespondBadRequest(c, "Order ID required", nil)
		return
	}

	// Placeholder - would fetch from order repository
	_ = userID

	common.RespondNotFound(c, "Order not found")
}

// CancelOrder cancels an order
// DELETE /api/v1/investment/orders/:id
func (h *InvestmentHandlers) CancelOrder(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	orderID := c.Param("id")
	if orderID == "" {
		common.RespondBadRequest(c, "Order ID required", nil)
		return
	}

	// Placeholder - would cancel via Alpaca client
	_ = userID

	c.JSON(http.StatusOK, gin.H{"message": "Order cancellation not yet implemented"})
}
