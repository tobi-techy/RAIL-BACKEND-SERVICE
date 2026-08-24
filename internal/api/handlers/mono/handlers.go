package mono

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	"go.uber.org/zap"
)

// Handlers exposes Mono open-banking endpoints (linking, transactions, analysis, deposits).
type Handlers struct {
	service *monosvc.Service
	logger  *zap.Logger
}

// maxTransactionLimit caps page size on transaction listings.
const maxTransactionLimit = 200

func NewHandlers(service *monosvc.Service, logger *zap.Logger) *Handlers {
	return &Handlers{service: service, logger: logger}
}

// --- Request Types ---

type initiateLinkRequest struct {
	CustomerName  string `json:"customer_name"`
	CustomerEmail string `json:"customer_email"`
	RedirectURL   string `json:"redirect_url"`
}

type completeLinkRequest struct {
	Code string `json:"code" binding:"required"`
}

type initiateDepositRequest struct {
	AccountID   uuid.UUID `json:"account_id" binding:"required"`
	AmountKobo  int64     `json:"amount_kobo" binding:"required,min=1"`
	Description string    `json:"description"`
	Reference   string    `json:"reference"`
	RedirectURL string    `json:"redirect_url"`
}

// --- Account Linking ---

// InitiateLink godoc
// @Summary Initiate Mono account linking
// @Description Starts the Mono Connect widget flow and returns the redirect URL
// @Tags mono
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body initiateLinkRequest true "Linking request"
// @Router /api/v1/mono/link/initiate [post]
func (h *Handlers) InitiateLink(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	var req initiateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
		return
	}

	if req.RedirectURL == "" {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "redirect_url is required", nil)
		return
	}

	redirectURL, err := h.service.InitiateLinking(c.Request.Context(), userID, req.CustomerName, req.CustomerEmail, req.RedirectURL)
	if err != nil {
		h.logger.Error("Mono initiate linking failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.RespondError(c, http.StatusBadGateway, "MONO_LINK_FAILED", "failed to initiate account linking", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"mono_url":     redirectURL,
		"redirect_url": redirectURL,
	})
}

// CompleteLink godoc
// @Summary Complete Mono account linking
// @Description Exchanges the Mono Connect widget code for a persistent account ID
// @Tags mono
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body completeLinkRequest true "Code from Mono widget"
// @Router /api/v1/mono/link/complete [post]
func (h *Handlers) CompleteLink(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	var req completeLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "code is required", nil)
		return
	}

	account, err := h.service.CompleteLinking(c.Request.Context(), userID, req.Code)
	if err != nil {
		h.logger.Error("Mono complete linking failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.RespondError(c, http.StatusBadGateway, "MONO_LINK_FAILED", "failed to complete account linking", nil)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"account": account,
	})
}

// --- Accounts ---

// ListAccounts godoc
// @Summary List linked Mono accounts
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Router /api/v1/mono/accounts [get]
func (h *Handlers) ListAccounts(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	accounts, err := h.service.ListLinkedAccounts(c.Request.Context(), userID)
	if err != nil {
		common.RespondError(c, http.StatusInternalServerError, "MONO_LIST_FAILED", "failed to list accounts", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accounts": accounts,
		"count":    len(accounts),
	})
}

// SyncAccount godoc
// @Summary Sync transactions for a linked account
// @Description Fetches latest transactions from Mono and imports them
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Param id path string true "Account ID"
// @Router /api/v1/mono/accounts/{id}/sync [post]
func (h *Handlers) SyncAccount(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid account ID", nil)
		return
	}

	imported, err := h.service.SyncAccount(c.Request.Context(), userID, accountID)
	if err != nil {
		h.logger.Error("Mono sync failed", zap.Error(err), zap.String("account_id", accountID.String()))
		common.RespondError(c, http.StatusBadGateway, "MONO_SYNC_FAILED", "failed to sync account", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"imported": imported,
		"message":  fmt.Sprintf("Synced %d new transactions", imported),
	})
}

// GetTransactions godoc
// @Summary Get imported transactions for a linked account
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Param id path string true "Account ID"
// @Param limit query int false "Page size (default 50, max 200)"
// @Param offset query int false "Offset"
// @Router /api/v1/mono/accounts/{id}/transactions [get]
func (h *Handlers) GetTransactions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid account ID", nil)
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit <= 0 {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "limit must be a positive integer", nil)
		return
	}
	if limit > maxTransactionLimit {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("limit cannot exceed %d", maxTransactionLimit), nil)
		return
	}

	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "offset must be a non-negative integer", nil)
		return
	}

	txns, err := h.service.GetTransactions(c.Request.Context(), userID, accountID, limit, offset)
	if err != nil {
		common.RespondError(c, http.StatusInternalServerError, "MONO_TXN_FAILED", "failed to get transactions", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transactions": txns,
		"count":        len(txns),
	})
}

// UnlinkAccount godoc
// @Summary Unlink a Mono account
// @Description Disconnects the bank account from Mono
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Param id path string true "Account ID"
// @Router /api/v1/mono/accounts/{id} [delete]
func (h *Handlers) UnlinkAccount(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid account ID", nil)
		return
	}

	if err := h.service.UnlinkAccount(c.Request.Context(), userID, accountID); err != nil {
		h.logger.Error("Mono unlink failed", zap.Error(err), zap.String("account_id", accountID.String()))
		common.RespondError(c, http.StatusBadGateway, "MONO_UNLINK_FAILED", "failed to unlink account", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Account unlinked successfully",
	})
}

// --- Spending Analysis ---

// GetAnalysis godoc
// @Summary Get spending analysis from Mono data
// @Description Returns spending breakdown by category, income vs expense, and savings rate
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Param days query int false "Analysis period in days (default 30)"
// @Router /api/v1/mono/analysis [get]
func (h *Handlers) GetAnalysis(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days <= 0 || days > 365 {
		days = 30
	}

	analysis, err := h.service.GetSpendingAnalysis(c.Request.Context(), userID, days)
	if err != nil {
		common.RespondError(c, http.StatusInternalServerError, "MONO_ANALYSIS_FAILED", "failed to get spending analysis", nil)
		return
	}

	c.JSON(http.StatusOK, analysis)
}

// --- DirectPay (Deposits) ---

// InitiateDeposit godoc
// @Summary Initiate a deposit via Mono DirectPay
// @Description Starts a one-time debit from the user's linked bank account
// @Tags mono
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body initiateDepositRequest true "Deposit request"
// @Router /api/v1/mono/deposit/initiate [post]
func (h *Handlers) InitiateDeposit(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	var req initiateDepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
		return
	}

	// Generate an unguessable reference if not provided. Never derive it from
	// the user ID — references appear in URLs and webhook payloads.
	reference := req.Reference
	if reference == "" {
		reference = fmt.Sprintf("rail-deposit-%s", uuid.NewString())
	}

	// Extract customer details from context if available.
	email, _ := c.Get("email")
	name, _ := c.Get("name")
	emailStr, _ := email.(string)
	nameStr, _ := name.(string)

	pmt, err := h.service.InitiateDeposit(c.Request.Context(), userID, req.AccountID, req.AmountKobo,
		req.Description, reference, req.RedirectURL, emailStr, nameStr)
	if err != nil {
		h.logger.Error("Mono initiate deposit failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.RespondError(c, http.StatusBadGateway, "MONO_DEPOSIT_FAILED", "failed to initiate deposit", nil)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"payment":      pmt,
		"approval_url": pmt.ApprovalURL,
		"mono_url":     pmt.ApprovalURL,
	})
}

// VerifyDeposit godoc
// @Summary Verify a Mono DirectPay deposit
// @Description Checks the payment status with Mono
// @Tags mono
// @Produce json
// @Security BearerAuth
// @Param reference path string true "Payment reference"
// @Router /api/v1/mono/deposit/{reference}/verify [get]
func (h *Handlers) VerifyDeposit(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "user not authenticated", nil)
		return
	}

	reference := c.Param("reference")
	if reference == "" {
		common.RespondError(c, http.StatusBadRequest, "BAD_REQUEST", "reference is required", nil)
		return
	}

	pmt, err := h.service.VerifyDeposit(c.Request.Context(), userID, reference)
	if err != nil {
		h.logger.Error("Mono verify deposit failed", zap.Error(err), zap.String("reference", reference))
		common.RespondError(c, http.StatusBadGateway, "MONO_VERIFY_FAILED", "failed to verify deposit", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payment": pmt,
	})
}
