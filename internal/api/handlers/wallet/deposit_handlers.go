package wallet

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Unified deposit request/response types

// CreateDepositRequest represents a unified deposit creation request
type CreateDepositRequest struct {
	Type            string `json:"type" binding:"required,oneof=crypto fiat"` // "crypto" or "fiat"
	Chain           string `json:"chain,omitempty"`                           // required for crypto
	AlpacaAccountID string `json:"alpaca_account_id,omitempty"`               // required for fiat
}

// CreateDepositResponse represents the unified deposit creation response
type CreateDepositResponse struct {
	DepositID string `json:"deposit_id,omitempty"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Address   string `json:"address,omitempty"` // for crypto
	Chain     string `json:"chain,omitempty"`   // for crypto
	Message   string `json:"message,omitempty"` // instructions
}

// DepositDetailResponse represents a single deposit
type DepositDetailResponse struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"` // crypto or fiat
	Chain       string  `json:"chain,omitempty"`
	TxHash      string  `json:"tx_hash,omitempty"`
	Amount      string  `json:"amount"`
	Status      string  `json:"status"`
	Currency    string  `json:"currency"`
	ConfirmedAt *string `json:"confirmed_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// CreateDeposit handles POST /api/v1/deposits
// Unified deposit creation - routes to crypto or fiat backend based on type
func (h *WalletFundingHandlers) CreateDeposit(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req CreateDepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request: type is required (crypto or fiat)"})
		return
	}

	ctx := c.Request.Context()

	switch req.Type {
	case "crypto":
		if req.Chain == "" {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Chain is required for crypto deposits"})
			return
		}
		chain := entities.Chain(strings.ToUpper(strings.TrimSpace(req.Chain)))
		validChains := map[entities.Chain]bool{
			entities.ChainSOLDevnet: true,
			entities.ChainMATICAmoy: true,
			entities.ChainAVAXFuji:  true,
		}
		if !validChains[chain] {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_CHAIN",
				Message: "Unsupported chain. Supported: SOL-DEVNET, MATIC-AMOY, AVAX-FUJI",
			})
			return
		}
		resp, err := h.fundingService.CreateDepositAddress(ctx, userUUID, chain)
		if err != nil {
			h.logger.Error("Failed to create deposit address", "error", err, "user_id", userUUID)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSIT_ERROR", Message: "Failed to create deposit address"})
			return
		}
		c.JSON(http.StatusCreated, CreateDepositResponse{
			Type:    "crypto",
			Status:  "pending",
			Address: resp.Address,
			Chain:   string(resp.Chain),
			Message: "Send USDC to the address below",
		})

	case "fiat":
		if strings.TrimSpace(req.AlpacaAccountID) == "" {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_REQUEST",
				Message: "alpaca_account_id is required for fiat deposits",
			})
			return
		}
		// Create or retrieve virtual account for fiat deposits
		resp, err := h.fundingService.CreateVirtualAccount(ctx, &entities.CreateVirtualAccountRequest{
			UserID:          userUUID,
			AlpacaAccountID: strings.TrimSpace(req.AlpacaAccountID),
		})
		if err != nil {
			if strings.Contains(err.Error(), "does not belong to authenticated user") {
				c.JSON(http.StatusForbidden, entities.ErrorResponse{Code: "ALPACA_ACCOUNT_FORBIDDEN", Message: "Alpaca account does not belong to authenticated user"})
				return
			}
			h.logger.Error("Failed to create fiat deposit", "error", err, "user_id", userUUID)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSIT_ERROR", Message: "Failed to initiate fiat deposit"})
			return
		}
		c.JSON(http.StatusCreated, CreateDepositResponse{
			Type:      "fiat",
			Status:    "pending",
			Message:   "Wire funds to your virtual account",
			DepositID: resp.VirtualAccount.ID.String(),
		})
	default:
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "type must be either crypto or fiat",
		})
	}
}

// ListDeposits handles GET /api/v1/deposits
func (h *WalletFundingHandlers) ListDeposits(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to list deposits", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSITS_ERROR", Message: "Failed to retrieve deposits"})
		return
	}

	deposits := make([]DepositDetailResponse, 0, len(confirmations))
	for _, conf := range confirmations {
		depositType := "crypto"
		currency := "USDC"
		chain := string(conf.Chain)
		if chain == "" || strings.EqualFold(chain, "fiat") || strings.HasPrefix(strings.ToLower(conf.Status), "off_ramp") || strings.EqualFold(conf.Status, "broker_funded") {
			depositType = "fiat"
			currency = "USD"
		}
		d := DepositDetailResponse{
			ID:        conf.ID.String(),
			Type:      depositType,
			Chain:     chain,
			TxHash:    conf.TxHash,
			Amount:    conf.Amount,
			Status:    conf.Status,
			Currency:  currency,
			CreatedAt: conf.ConfirmedAt.Format("2006-01-02T15:04:05Z"),
		}
		if !conf.ConfirmedAt.IsZero() {
			t := conf.ConfirmedAt.Format("2006-01-02T15:04:05Z")
			d.ConfirmedAt = &t
		}
		deposits = append(deposits, d)
	}

	c.JSON(http.StatusOK, gin.H{
		"deposits": deposits,
		"total":    len(deposits),
		"limit":    limit,
		"offset":   offset,
		"has_more": len(deposits) == limit,
	})
}

// GetDeposit handles GET /api/v1/deposits/:id
func (h *WalletFundingHandlers) GetDeposit(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	depositID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_ID", Message: "Invalid deposit ID"})
		return
	}

	// Use the deposit repo through the funding service's existing GetByTxHash pattern
	confirmation, err := h.fundingService.GetFundingConfirmationByID(c.Request.Context(), userUUID, depositID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "DEPOSIT_NOT_FOUND", Message: "Deposit not found"})
			return
		}
		h.logger.Error("Failed to get deposit", "error", err, "deposit_id", depositID, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSIT_ERROR", Message: "Failed to retrieve deposit"})
		return
	}

	depositType := "crypto"
	currency := "USDC"
	chain := string(confirmation.Chain)
	if chain == "" || strings.HasPrefix(strings.ToLower(confirmation.Status), "off_ramp") || strings.EqualFold(confirmation.Status, "broker_funded") {
		depositType = "fiat"
		currency = "USD"
	}

	resp := DepositDetailResponse{
		ID:        confirmation.ID.String(),
		Type:      depositType,
		Chain:     chain,
		TxHash:    confirmation.TxHash,
		Amount:    confirmation.Amount,
		Status:    confirmation.Status,
		Currency:  currency,
		CreatedAt: confirmation.ConfirmedAt.Format("2006-01-02T15:04:05Z"),
	}
	if !confirmation.ConfirmedAt.IsZero() {
		ts := confirmation.ConfirmedAt.Format("2006-01-02T15:04:05Z")
		resp.ConfirmedAt = &ts
	}
	c.JSON(http.StatusOK, resp)
}
