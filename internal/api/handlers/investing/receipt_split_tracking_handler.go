package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// ReceiptSplitTrackingHandler handles split tracking and collection.
type ReceiptSplitTrackingHandler struct {
	repo   *repositories.ReceiptSplitRepository
	logger *zap.Logger
}

func NewReceiptSplitTrackingHandler(repo *repositories.ReceiptSplitRepository, logger *zap.Logger) *ReceiptSplitTrackingHandler {
	return &ReceiptSplitTrackingHandler{repo: repo, logger: logger}
}

// ListSplits handles GET /v1/ai/receipts/splits
func (h *ReceiptSplitTrackingHandler) ListSplits(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	status := c.Query("status")
	splits, err := h.repo.ListByUser(c.Request.Context(), userID, status, 50)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": splits})
}

// GetSplit handles GET /v1/ai/receipts/splits/:id
func (h *ReceiptSplitTrackingHandler) GetSplit(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	splitID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid split ID")
		return
	}

	split, err := h.repo.GetByID(c.Request.Context(), userID, splitID)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Split not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": split})
}

// SendReminder handles POST /v1/ai/receipts/splits/:id/remind
func (h *ReceiptSplitTrackingHandler) SendReminder(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	splitID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid split ID")
		return
	}

	split, err := h.repo.GetByID(c.Request.Context(), userID, splitID)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Split not found")
		return
	}

	reminded := 0
	for _, p := range split.Participants {
		if p.Status == "pending" || p.Status == "requested" {
			if err := h.repo.IncrementReminder(c.Request.Context(), p.ID); err == nil {
				reminded++
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"reminded": reminded})
}

// MarkPaid handles POST /v1/ai/receipts/splits/:id/participants/:pid/paid
func (h *ReceiptSplitTrackingHandler) MarkPaid(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	splitID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid split ID")
		return
	}

	participantID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid participant ID")
		return
	}

	// Verify ownership
	_, err = h.repo.GetByID(c.Request.Context(), userID, splitID)
	if err != nil {
		common.SendNotFound(c, common.ErrCodeNotFound, "Split not found")
		return
	}

	if err := h.repo.UpdateParticipantStatus(c.Request.Context(), participantID, "paid"); err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	// Check if all participants are paid → update split status
	pending, _ := h.repo.GetPendingParticipants(c.Request.Context(), splitID)
	if len(pending) == 0 {
		h.repo.UpdateSplitStatus(c.Request.Context(), splitID, "collected")
	} else {
		h.repo.UpdateSplitStatus(c.Request.Context(), splitID, "partial")
	}

	c.JSON(http.StatusOK, gin.H{"status": "paid"})
}
