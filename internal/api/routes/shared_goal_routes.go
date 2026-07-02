package routes

import (
	"github.com/gin-gonic/gin"
	investinghandlers "github.com/rail-service/rail_service/internal/api/handlers/investing"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	"go.uber.org/zap"
)

// RegisterSharedGoalRoutes exposes collaborative savings goals. The service is
// also used by Miriam (CreateGoalFromAI); these routes are the in-app surface
// the mobile client calls.
func RegisterSharedGoalRoutes(protected *gin.RouterGroup, svc *sharedgoal.Service, log *zap.Logger) {
	h := investinghandlers.NewSharedGoalHandler(svc, log)

	goals := protected.Group("/goals/shared")
	{
		goals.POST("", h.CreateGoal)
		goals.GET("", h.ListGoals)
		goals.GET("/invites", h.GetInvites)
		goals.POST("/invites/:id/respond", h.RespondToInvite)
		goals.GET("/:id", h.GetGoal)
		goals.POST("/:id/contribute", h.Contribute)
		goals.POST("/:id/invite", h.InviteMembers)
		goals.GET("/:id/leaderboard", h.GetLeaderboard)
		goals.GET("/:id/contributions", h.GetContributions)
		goals.POST("/:id/leave", h.LeaveGoal)
	}
}
