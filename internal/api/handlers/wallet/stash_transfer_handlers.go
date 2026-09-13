package wallet

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// StashTransferer is the ledger surface chat-channel stash moves need.
// Implemented by *ledger.Service. These are the user-scoped methods that
// already enforce the 90-day stash lock — not the admin/emergency bypasses.
type StashTransferer interface {
	TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	TransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
}

// StashTransferHandlers exposes OTP-safe stash↔spending moves for the Python
// agent (and any other non-app channel that authenticates with a normal JWT,
// including agent tokens). These routes are deliberately NOT behind
// RequirePasscodeSession: messaging cannot produce a passcode session, and
// identity is proven by the email-OTP confirmation Go stages before Python
// is allowed to call these endpoints.
type StashTransferHandlers struct {
	ledger StashTransferer
	logger *logger.Logger
}

func NewStashTransferHandlers(ledger StashTransferer, logger *logger.Logger) *StashTransferHandlers {
	return &StashTransferHandlers{ledger: ledger, logger: logger}
}

// StashTransferRequest matches FundStash: amount as a decimal string.
type StashTransferRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// TransferSpendingToStash handles POST /api/v1/funding/stash/from-spending
func (h *StashTransferHandlers) TransferSpendingToStash(c *gin.Context) {
	h.execute(c, "spending_to_stash", func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, key string) error {
		return h.ledger.TransferSpendingToStash(ctx, userID, amount, key)
	})
}

// TransferStashToSpending handles POST /api/v1/funding/stash/to-spending
func (h *StashTransferHandlers) TransferStashToSpending(c *gin.Context) {
	h.execute(c, "stash_to_spending", func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, key string) error {
		return h.ledger.TransferStashToSpending(ctx, userID, amount, key)
	})
}

func (h *StashTransferHandlers) execute(
	c *gin.Context,
	direction string,
	fn func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, key string) error,
) {
	userID, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User not authenticated")
		return
	}
	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		common.SendInternalError(c, common.ErrCodeInternalError, "Invalid user ID format")
		return
	}

	var req StashTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request: "+err.Error())
		return
	}
	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Missing or invalid idempotency key")
		return
	}
	if idempotencyKey == "" {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Idempotency key is required for fund transfers")
		return
	}

	if err := fn(c.Request.Context(), userUUID, amount, idempotencyKey); err != nil {
		h.mapTransferError(c, userUUID, direction, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "completed",
		"amount":    amount.String(),
		"direction": direction,
	})
}

func (h *StashTransferHandlers) mapTransferError(c *gin.Context, userID uuid.UUID, direction string, err error) {
	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "insufficient"):
		common.SendBadRequest(c, common.ErrCodeInsufficientFunds, errMsg)
	case strings.Contains(errMsg, "locked"):
		common.SendBadRequest(c, "STASH_LOCKED", errMsg)
	case strings.Contains(errMsg, "invalid transfer amount"):
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, errMsg)
	default:
		if h.logger != nil {
			h.logger.Error("Stash transfer failed", "error", err, "user_id", userID, "direction", direction)
		}
		common.SendInternalError(c, "STASH_TRANSFER_ERROR", "Failed to complete stash transfer")
	}
}
