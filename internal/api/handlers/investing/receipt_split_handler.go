package investing

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// ReceiptSplitHandler handles splitting receipts with friends via P2P.
type ReceiptSplitHandler struct {
	receiptRepo      *repositories.ReceiptRepository
	receiptSplitRepo *repositories.ReceiptSplitRepository
	p2pService       *p2pservice.Service
	logger           *zap.Logger
}

func NewReceiptSplitHandler(receiptRepo *repositories.ReceiptRepository, receiptSplitRepo *repositories.ReceiptSplitRepository, p2pService *p2pservice.Service, logger *zap.Logger) *ReceiptSplitHandler {
	return &ReceiptSplitHandler{receiptRepo: receiptRepo, receiptSplitRepo: receiptSplitRepo, p2pService: p2pService, logger: logger}
}

type splitRequest struct {
	Participants  []string          `json:"participants" binding:"required,min=1"`
	SplitType     string            `json:"split_type" binding:"required,oneof=equal custom"`
	CustomAmounts map[string]string `json:"custom_amounts,omitempty"`
	Message       string            `json:"message,omitempty"`
}

type splitEntry struct {
	RailTag    string `json:"rail_tag"`
	Amount     string `json:"amount"`
	Status     string `json:"status"`
	TransferID string `json:"transfer_id"`
}

// SplitReceipt handles POST /v1/ai/receipts/:id/split
func (h *ReceiptSplitHandler) SplitReceipt(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	if h.receiptRepo == nil || h.p2pService == nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "Service unavailable")
		return
	}

	receiptID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid receipt ID")
		return
	}

	var req splitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	receipt, err := h.receiptRepo.GetByID(c.Request.Context(), userID, receiptID)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Receipt not found")
		return
	}

	// Prevent re-splitting a receipt that was already split
	if h.receiptSplitRepo != nil {
		if existing, err := h.receiptSplitRepo.GetSplitByReceipt(c.Request.Context(), userID, receiptID); err == nil && existing != nil {
			common.SendBadRequest(c, common.ErrCodeInvalidRequest, "This receipt has already been split")
			return
		}
	}

	if !receipt.Amount.IsPositive() {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Receipt has no valid amount to split")
		return
	}

	totalPeople := len(req.Participants) + 1
	splits := make([]splitEntry, 0, len(req.Participants))
	var yourShare decimal.Decimal

	if req.SplitType == "equal" {
		share := receipt.Amount.Div(decimal.NewFromInt(int64(totalPeople))).Round(2)
		// Assign rounding remainder to the payer so no cents are lost
		othersTotal := share.Mul(decimal.NewFromInt(int64(len(req.Participants))))
		yourShare = receipt.Amount.Sub(othersTotal)

		for _, tag := range req.Participants {
			tag = strings.TrimPrefix(strings.TrimSpace(tag), "@")
			if tag == "" {
				continue
			}
			note := req.Message
			if note == "" {
				note = "Receipt split"
			}
			resp, p2pErr := h.p2pService.Send(c.Request.Context(), userID, &entities.P2PSendRequest{
				Identifier:     tag,
				Amount:         share.StringFixed(2),
				Note:           note,
				IdempotencyKey: fmt.Sprintf("split-%s-%s-%s", receiptID.String(), tag, share.StringFixed(2)),
			})
			entry := splitEntry{RailTag: "@" + tag, Amount: share.StringFixed(2), Status: "failed"}
			if p2pErr == nil && resp.Transfer != nil {
				entry.Status = string(resp.Transfer.Status)
				entry.TransferID = resp.Transfer.ID.String()
			} else if p2pErr != nil {
				h.logger.Warn("split P2P send failed", zap.String("tag", tag), zap.Error(p2pErr))
			}
			splits = append(splits, entry)
		}
	} else {
		// Custom split — validate all participants have amounts
		customTotal := decimal.Zero
		for _, tag := range req.Participants {
			tag = strings.TrimPrefix(strings.TrimSpace(tag), "@")
			if tag == "" {
				continue
			}
			amtStr, ok := req.CustomAmounts[tag]
			if !ok {
				amtStr, ok = req.CustomAmounts["@"+tag]
			}
			if !ok || amtStr == "" {
				common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Missing custom amount for participant: @"+tag)
				return
			}
			amt, err := decimal.NewFromString(amtStr)
			if err != nil || amt.LessThanOrEqual(decimal.Zero) {
				common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Invalid custom amount for participant: @"+tag)
				return
			}
			customTotal = customTotal.Add(amt)
		}
		if customTotal.GreaterThan(receipt.Amount) {
			common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Custom split amounts exceed receipt total")
			return
		}

		yourShare = receipt.Amount.Sub(customTotal)
		for _, tag := range req.Participants {
			tag = strings.TrimPrefix(strings.TrimSpace(tag), "@")
			if tag == "" {
				continue
			}
			amtStr, ok := req.CustomAmounts[tag]
			if !ok {
				amtStr, ok = req.CustomAmounts["@"+tag]
			}
			if !ok || amtStr == "" {
				continue
			}
			amt, err := decimal.NewFromString(amtStr)
			if err != nil || amt.LessThanOrEqual(decimal.Zero) {
				continue
			}
			note := req.Message
			if note == "" {
				note = "Receipt split"
			}
			resp, sendErr := h.p2pService.Send(c.Request.Context(), userID, &entities.P2PSendRequest{
				Identifier:     tag,
				Amount:         amt.StringFixed(2),
				Note:           note,
				IdempotencyKey: fmt.Sprintf("split-%s-%s-%s", receiptID.String(), tag, amt.StringFixed(2)),
			})
			entry := splitEntry{RailTag: "@" + tag, Amount: amt.StringFixed(2), Status: "failed"}
			if sendErr == nil && resp.Transfer != nil {
				entry.Status = string(resp.Transfer.Status)
				entry.TransferID = resp.Transfer.ID.String()
			} else if sendErr != nil {
				h.logger.Warn("split P2P send failed", zap.String("tag", tag), zap.Error(sendErr))
			}
			splits = append(splits, entry)
		}
	}

	// Determine if any transfers failed for partial success indication
	failedCount := 0
	for _, s := range splits {
		if s.Status == "failed" {
			failedCount++
		}
	}

	resp := gin.H{
		"receipt_id":   receipt.ID.String(),
		"total_amount": receipt.Amount.StringFixed(2),
		"your_share":   yourShare.StringFixed(2),
		"splits":       splits,
	}
	if failedCount > 0 && failedCount < len(splits) {
		resp["partial_success"] = true
		resp["failed_count"] = failedCount
	}

	c.JSON(http.StatusOK, resp)
}
