package routes

import (
	"github.com/gin-gonic/gin"
	gameplayhandlers "github.com/rail-service/rail_service/internal/api/handlers/gameplay"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
)

// SetupGameplayRoutes registers gameplay and subscription endpoints
func SetupGameplayRoutes(rg *gin.RouterGroup, container *di.Container) {
	if container.GameplayXPService == nil {
		return
	}

	h := gameplayhandlers.NewHandlers(
		container.GameplayXPService,
		container.GameplayStreakService,
		container.GameplayChallengeService,
		container.GameplayAchievementService,
		container.SubscriptionService,
		container.ZapLog,
	)

	gp := rg.Group("/gameplay")
	{
		gp.GET("/profile", h.GetProfile)
		gp.GET("/streaks", h.GetStreaks)
		gp.GET("/xp", h.GetXP)
		gp.GET("/xp/history", h.GetXPHistory)
		gp.GET("/challenges", h.GetChallenges)
		gp.GET("/achievements", h.GetAchievements)

		// Leaderboard — Pro only
		if container.SubscriptionService != nil {
			gp.GET("/leaderboard/:type", middleware.ProGate(container.SubscriptionService), h.GetLeaderboard)
		}
	}

	sub := rg.Group("/subscription")
	{
		sub.GET("", h.GetSubscription)
		sub.POST("", h.Subscribe)
		sub.DELETE("", h.CancelSubscription)
	}
}
