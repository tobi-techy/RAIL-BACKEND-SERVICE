package investing

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
)

// HouseholdHandler handles shared household expense tracking.
type HouseholdHandler struct {
	db         *sqlx.DB
	p2pService *p2pservice.Service
	logger     *zap.Logger
}

func NewHouseholdHandler(db *sqlx.DB, p2pService *p2pservice.Service, logger *zap.Logger) *HouseholdHandler {
	return &HouseholdHandler{db: db, p2pService: p2pService, logger: logger}
}

type createGroupRequest struct {
	Name    string   `json:"name" binding:"required,max=100"`
	Members []string `json:"members"` // rail_tags
}

// CreateGroup handles POST /v1/household/groups
func (h *HouseholdHandler) CreateGroup(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	const maxMembers = 50
	if len(req.Members) > maxMembers {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Too many members (max 50)")
		return
	}

	tx, err := h.db.BeginTxx(c.Request.Context(), nil)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	groupID := uuid.New()
	_, err = tx.ExecContext(c.Request.Context(),
		`INSERT INTO household_groups (id, name, created_by) VALUES ($1, $2, $3)`,
		groupID, req.Name, userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to create group")
		return
	}

	// Add creator as member
	if _, err := tx.ExecContext(c.Request.Context(),
		`INSERT INTO household_members (group_id, user_id) VALUES ($1, $2)`,
		groupID, userID); err != nil {
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to add creator as member")
		return
	}

	// Resolve and add members by rail_tag
	added := []string{}
	if h.p2pService != nil {
		for _, tag := range req.Members {
			tag = strings.TrimPrefix(strings.TrimSpace(tag), "@")
			if tag == "" {
				continue
			}
			lookup, err := h.p2pService.LookupRecipient(c.Request.Context(), tag)
			if err != nil || !lookup.Found || lookup.User == nil {
				continue
			}
			_, err = tx.ExecContext(c.Request.Context(),
				`INSERT INTO household_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				groupID, lookup.User.ID)
			if err == nil {
				added = append(added, "@"+tag)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to commit")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"group_id":      groupID.String(),
		"name":          req.Name,
		"members_added": added,
	})
}

type shareReceiptRequest struct {
	ReceiptID string `json:"receipt_id" binding:"required"`
}

// ShareReceipt handles POST /v1/household/groups/:id/receipts
func (h *HouseholdHandler) ShareReceipt(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid group ID")
		return
	}

	var req shareReceiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	receiptID, err := uuid.Parse(req.ReceiptID)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid receipt ID")
		return
	}

	// Verify membership
	var isMember bool
	if err := h.db.GetContext(c.Request.Context(), &isMember,
		`SELECT EXISTS(SELECT 1 FROM household_members WHERE group_id=$1 AND user_id=$2)`,
		groupID, userID); err != nil {
		h.logger.Error("failed to check membership", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to verify membership")
		return
	}
	if !isMember {
		common.SendForbidden(c, "Not a member of this group")
		return
	}

	// Verify the receipt belongs to the caller
	var receiptOwnerID uuid.UUID
	if err := h.db.GetContext(c.Request.Context(), &receiptOwnerID,
		`SELECT user_id FROM receipt_scans WHERE id = $1`, receiptID); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Receipt not found")
		return
	}
	if receiptOwnerID != userID {
		common.SendForbidden(c, "You can only share your own receipts")
		return
	}

	_, err = h.db.ExecContext(c.Request.Context(),
		`INSERT INTO household_receipts (receipt_id, group_id, shared_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		receiptID, groupID, userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to share receipt")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "shared", "receipt_id": receiptID.String(), "group_id": groupID.String()})
}

// GetSummary handles GET /v1/household/groups/:id/summary
func (h *HouseholdHandler) GetSummary(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid group ID")
		return
	}

	// Verify membership
	var isMember bool
	if err := h.db.GetContext(c.Request.Context(), &isMember,
		`SELECT EXISTS(SELECT 1 FROM household_members WHERE group_id=$1 AND user_id=$2)`,
		groupID, userID); err != nil {
		h.logger.Error("failed to check membership", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to verify membership")
		return
	}
	if !isMember {
		common.SendForbidden(c, "Not a member of this group")
		return
	}

	// Get member count
	var memberCount int
	if err := h.db.GetContext(c.Request.Context(), &memberCount,
		`SELECT COUNT(*) FROM household_members WHERE group_id=$1`, groupID); err != nil {
		h.logger.Error("failed to get member count", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get group data")
		return
	}
	if memberCount == 0 {
		memberCount = 1
	}

	// Get total per sharer
	type sharerTotal struct {
		SharedBy uuid.UUID       `db:"shared_by"`
		Total    decimal.Decimal `db:"total"`
	}
	var totals []sharerTotal
	if err := h.db.SelectContext(c.Request.Context(), &totals, `
		SELECT hr.shared_by, COALESCE(SUM(rs.amount), 0) AS total
		FROM household_receipts hr
		JOIN receipt_scans rs ON rs.id = hr.receipt_id
		WHERE hr.group_id = $1
		GROUP BY hr.shared_by`, groupID); err != nil {
		h.logger.Error("failed to get sharer totals", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get group data")
		return
	}

	grandTotal := decimal.Zero
	paidMap := make(map[string]decimal.Decimal)
	for _, t := range totals {
		grandTotal = grandTotal.Add(t.Total)
		paidMap[t.SharedBy.String()] = t.Total
	}

	equalShare := decimal.Zero
	if memberCount > 0 {
		equalShare = grandTotal.Div(decimal.NewFromInt(int64(memberCount))).Round(2)
	}

	// Get all members
	var memberIDs []uuid.UUID
	if err := h.db.SelectContext(c.Request.Context(), &memberIDs,
		`SELECT user_id FROM household_members WHERE group_id=$1`, groupID); err != nil {
		h.logger.Error("failed to get member IDs", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get group data")
		return
	}

	type memberBalance struct {
		MemberIndex int    `json:"member_index"`
		IsYou       bool   `json:"is_you"`
		Paid        string `json:"paid"`
		Owes        string `json:"owes"`
		Balance     string `json:"balance"`
	}

	balances := make([]memberBalance, 0, len(memberIDs))
	for i, mid := range memberIDs {
		paid := paidMap[mid.String()]
		bal := paid.Sub(equalShare)
		balances = append(balances, memberBalance{
			MemberIndex: i + 1,
			IsYou:       mid == userID,
			Paid:        paid.StringFixed(2),
			Owes:        equalShare.StringFixed(2),
			Balance:     bal.StringFixed(2),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"group_id":     groupID.String(),
		"total":        grandTotal.StringFixed(2),
		"equal_share":  equalShare.StringFixed(2),
		"member_count": memberCount,
		"balances":     balances,
	})
}
