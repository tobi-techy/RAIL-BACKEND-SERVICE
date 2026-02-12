package wallet

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Unified deposit request/response types

// CreateDepositRequest represents a unified deposit creation request
type CreateDepositRequest struct {
	Type  string `json:"type" binding:"required,oneof=crypto fiat"` // "crypto" or "fiat"
	Chain string `json:"chain,omitempty"`                           // required for crypto
}

// CreateDepositResponse represents the unified deposit creation response
type CreateDepositResponse struct {
	DepositID string `json:"deposit_id,omitempty"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Address   string `json:"address,omitempty"`   // for crypto
	Chain     string `json:"chain,omitempty"`      // for crypto
	Message   string `json:"message,omitempty"`    // instructions
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
		resp, err := h.fundingService.CreateDepositAddress(ctx, userUUID, entities.Chain(req.Chain))
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
		// Create or retrieve virtual account for fiat deposits
		resp, err := h.fundingService.CreateVirtualAccount(ctx, &entities.CreateVirtualAccountRequest{UserID: userUUID})
		if err != nil {
			h.logger.Error("Failed to create fiat deposit", "error", err, "user_id", userUUID)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSIT_ERROR", Message: "Failed to initiate fiat deposit"})
			return
		}
		c.JSON(http.StatusCreated, CreateDepositResponse{
			Type:    "fiat",
			Status:  "pending",
			Message: "Wire funds to your virtual account",
			DepositID: resp.VirtualAccount.ID.String(),
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
	if limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to list deposits", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DEPOSITS_ERROR", Message: "Failed to retrieve deposits"})
		return
	}

	deposits := make([]DepositDetailResponse, 0, len(confirmations))
	for _, conf := range confirmations {
		d := DepositDetailResponse{
			ID:        conf.ID.String(),
			Type:      "crypto", // default; fiat deposits will have different chain markers
			Chain:     string(conf.Chain),
			TxHash:    conf.TxHash,
			Amount:    conf.Amount,
			Status:    conf.Status,
			Currency:  "USDC",
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
	_, err := common.GetUserID(c)
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
	// For now, we search the user's deposits and match by ID
	// TODO: Add GetByID to deposit repo for direct lookup
	_ = depositID
	c.JSON(http.StatusNotImplemented, entities.ErrorResponse{Code: "NOT_IMPLEMENTED", Message: "Direct deposit lookup coming soon - use GET /deposits to list"})
}
