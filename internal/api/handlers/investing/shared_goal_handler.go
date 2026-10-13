package investing

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/authz"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
)

// SharedGoalHandler handles collaborative goal endpoints.
type SharedGoalHandler struct {
	service *sharedgoal.Service
	logger  *zap.Logger
}

func NewSharedGoalHandler(service *sharedgoal.Service, logger *zap.Logger) *SharedGoalHandler {
	return &SharedGoalHandler{service: service, logger: logger}
}

// CreateGoal handles POST /v1/goals/shared
func (h *SharedGoalHandler) CreateGoal(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	var req sharedgoal.CreateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	goal, err := h.service.Create(c.Request.Context(), userID, &req)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": goal})
}

// ListGoals handles GET /v1/goals/shared
func (h *SharedGoalHandler) ListGoals(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goals, err := h.service.List(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": goals})
}

// GetGoal handles GET /v1/goals/shared/:id
func (h *SharedGoalHandler) GetGoal(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	goal, err := h.service.Get(c.Request.Context(), userID, id)
	if err != nil {
		h.sendGoalReadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": goal})
}

// Contribute handles POST /v1/goals/shared/:id/contribute
func (h *SharedGoalHandler) Contribute(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	var req sharedgoal.ContributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	contrib, err := h.service.Contribute(c.Request.Context(), userID, goalID, &req)
	if err != nil {
		h.sendGoalReadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": contrib})
}

// InviteMembers handles POST /v1/goals/shared/:id/invite
func (h *SharedGoalHandler) InviteMembers(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	var req struct {
		Tags    []string `json:"tags" binding:"required,min=1"`
		Message *string  `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	invites, err := h.service.InviteMembers(c.Request.Context(), userID, goalID, req.Tags, req.Message)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": invites})
}

// GetInvites handles GET /v1/goals/shared/invites
func (h *SharedGoalHandler) GetInvites(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	invites, err := h.service.GetPendingInvites(c.Request.Context(), userID)
	if err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": invites})
}

// RespondToInvite handles POST /v1/goals/shared/invites/:id/respond
func (h *SharedGoalHandler) RespondToInvite(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	inviteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid invite ID")
		return
	}

	var req struct {
		Accept bool `json:"accept"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	if err := h.service.RespondToInvite(c.Request.Context(), userID, inviteID, req.Accept); err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// GetLeaderboard handles GET /v1/goals/shared/:id/leaderboard
func (h *SharedGoalHandler) GetLeaderboard(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	members, err := h.service.GetLeaderboard(c.Request.Context(), userID, goalID)
	if err != nil {
		h.sendGoalReadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": members})
}

// GetContributions handles GET /v1/goals/shared/:id/contributions
func (h *SharedGoalHandler) GetContributions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	contribs, err := h.service.GetContributions(c.Request.Context(), userID, goalID, 50)
	if err != nil {
		h.sendGoalReadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": contribs})
}

// sendGoalReadError maps a read/contribute failure to a response. A caller who
// is not a member gets 404, never 403, so the API does not disclose that the
// goal exists.
func (h *SharedGoalHandler) sendGoalReadError(c *gin.Context, err error) {
	if errors.Is(err, authz.ErrNotAuthorized) {
		common.SendNotFound(c, common.ErrCodeNotFound, "Goal not found")
		return
	}
	common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
}

// LeaveGoal handles POST /v1/goals/shared/:id/leave
func (h *SharedGoalHandler) LeaveGoal(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid goal ID")
		return
	}

	if err := h.service.Leave(c.Request.Context(), userID, goalID); err != nil {
		common.SendInternalError(c, common.ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "left"})
}
