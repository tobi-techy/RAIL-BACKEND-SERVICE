package handlers

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// FundingHandlers handles funding-related operations
type FundingHandlers struct {
	fundingService *funding.Service
	validator      *validator.Validate
	logger         *logger.Logger
}

// NewFundingHandlers creates a new FundingHandlers instance
func NewFundingHandlers(fundingService *funding.Service, logger *logger.Logger) *FundingHandlers {
	return &FundingHandlers{
		fundingService: fundingService,
		validator:      validator.New(),
		logger:         logger,
	}
}

// CreateDepositAddress handles POST /api/v1/funding/deposit-address
func (h *FundingHandlers) CreateDepositAddress(c *gin.Context) {
	var req entities.DepositAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	response, err := h.fundingService.CreateDepositAddress(c.Request.Context(), userUUID, req.Chain)
	if err != nil {
		h.logger.Error("Failed to create deposit address",
			"error", err,
			"user_id", userUUID,
			"chain", req.Chain)
		SendInternalError(c, "DEPOSIT_ADDRESS_ERROR", "Failed to create deposit address")
		return
	}

	SendSuccess(c, response)
}

// GetFundingConfirmations handles GET /api/v1/funding/confirmations
func (h *FundingHandlers) GetFundingConfirmations(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	limit, offset := h.parsePagination(c, 20, 100)

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get funding confirmations",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "CONFIRMATIONS_ERROR", "Failed to retrieve funding confirmations")
		return
	}

	response := entities.FundingConfirmationsPage{
		Items:      confirmations,
		NextCursor: nil,
	}

	if len(confirmations) == limit {
		nextCursor := strconv.Itoa(offset + limit)
		response.NextCursor = &nextCursor
	}

	SendSuccess(c, response)
}

// GetBalances handles GET /api/v1/funding/balances
func (h *FundingHandlers) GetBalances(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	balances, err := h.fundingService.GetBalance(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get balances",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "BALANCES_ERROR", "Failed to retrieve balances")
		return
	}

	SendSuccess(c, balances)
}

// CreateVirtualAccount handles POST /api/v1/funding/virtual-account
func (h *FundingHandlers) CreateVirtualAccount(c *gin.Context) {
	var req entities.CreateVirtualAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	req.UserID = userUUID

	if req.AlpacaAccountID == "" {
		SendBadRequest(c, ErrCodeInvalidRequest, "Alpaca account ID is required")
		return
	}

	response, err := h.fundingService.CreateVirtualAccount(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to create virtual account",
			"error", err,
			"user_id", userUUID,
			"alpaca_account_id", req.AlpacaAccountID)

		if errMsg := err.Error(); strings.Contains(errMsg, "already exists") {
			SendConflict(c, "VIRTUAL_ACCOUNT_EXISTS", "Virtual account already exists for this Alpaca account")
			return
		}

		if strings.Contains(err.Error(), "not active") {
			SendBadRequest(c, "ALPACA_ACCOUNT_INACTIVE", "Alpaca account is not active")
			return
		}

		SendInternalError(c, "VIRTUAL_ACCOUNT_ERROR", "Failed to create virtual account")
		return
	}

	SendCreated(c, response)
}

// GetTransactionHistory handles GET /api/v1/funding/transactions
func (h *FundingHandlers) GetTransactionHistory(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	limit, offset := h.parsePagination(c, 20, 100)

	filter := h.parseTransactionFilter(c)

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get transaction history",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "TRANSACTION_HISTORY_ERROR", "Failed to retrieve transaction history")
		return
	}

	transactions := h.convertToUnifiedTransactions(confirmations, userUUID, filter)

	response := &funding.TransactionHistoryResponse{
		Transactions: transactions,
		Total:        len(transactions),
		Limit:        limit,
		Offset:       offset,
		HasMore:      len(transactions) == limit,
	}

	SendSuccess(c, response)
}

// Helper methods

func (h *FundingHandlers) parsePagination(c *gin.Context, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if l, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultLimit))); l > 0 {
		limit = l
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	offset = 0
	if cursor := c.Query("cursor"); cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 {
			offset = o
		}
	}
	if o, _ := strconv.Atoi(c.Query("offset")); o >= 0 {
		offset = o
	}

	return limit, offset
}

func (h *FundingHandlers) parseTransactionFilter(c *gin.Context) *funding.TransactionFilter {
	var filter *funding.TransactionFilter

	if typeParam := c.Query("type"); typeParam != "" {
		filter = &funding.TransactionFilter{
			Types: strings.Split(typeParam, ","),
		}
	}

	if statusParam := c.Query("status"); statusParam != "" {
		if filter == nil {
			filter = &funding.TransactionFilter{}
		}
		filter.Status = &statusParam
	}

	return filter
}

func (h *FundingHandlers) convertToUnifiedTransactions(confirmations []*entities.FundingConfirmation, userID any, filter *funding.TransactionFilter) []*funding.UnifiedTransaction {
	transactions := make([]*funding.UnifiedTransaction, 0, len(confirmations))

	for _, conf := range confirmations {
		tx := &funding.UnifiedTransaction{
			ID:          conf.ID,
			Type:        "deposit",
			Status:      conf.Status,
			Amount:      mustParseDecimalSafe(conf.Amount),
			Currency:    "USDC",
			Description: "USDC deposit",
			Chain:       string(conf.Chain),
			TxHash:      conf.TxHash,
			CreatedAt:   conf.ConfirmedAt,
		}

		// Apply type filter if present
		if filter != nil && len(filter.Types) > 0 {
			matched := false
			for _, t := range filter.Types {
				if t == tx.Type {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Apply status filter if present
		if filter != nil && filter.Status != nil && *filter.Status != tx.Status {
			continue
		}

		transactions = append(transactions, tx)
	}

	return transactions
}

func mustParseDecimalSafe(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}
