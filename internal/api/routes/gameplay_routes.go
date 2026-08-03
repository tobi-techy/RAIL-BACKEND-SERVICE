package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	h.SetHeatmapRepo(container.GameplayRepo)

	// Wire new V2 services
	if container.GameplayRingsService != nil {
		h.SetRingsService(container.GameplayRingsService)
	}
	if container.GameplayBoostService != nil {
		h.SetBoostService(container.GameplayBoostService)
	}
	if container.GameplayPointsService != nil {
		h.SetPointsService(container.GameplayPointsService)
	}
	if container.GameplayGraceDayService != nil {
		h.SetGraceDayService(container.GameplayGraceDayService)
	}
	if container.GameplayRecapService != nil {
		h.SetRecapService(container.GameplayRecapService)
	}

	gp := rg.Group("/gameplay")
	{
		gp.GET("/profile", h.GetProfile)
		gp.GET("/streaks", h.GetStreaks)
		gp.GET("/activity-heatmap", h.GetActivityHeatmap)
		gp.GET("/xp", h.GetXP)
		gp.GET("/xp/history", h.GetXPHistory)
		gp.GET("/challenges", h.GetChallenges)
		gp.GET("/achievements", h.GetAchievements)

		// Rings (Apple Fitness style)
		gp.GET("/rings", h.GetRings)

		// Boosts (Cash App style)
		gp.GET("/boosts", h.GetBoosts)
		gp.POST("/boosts/activate", h.ActivateBoost)
		gp.GET("/boosts/history", h.GetBoostHistory)

		// Rail Points (Starbucks style)
		gp.GET("/points", h.GetRailPoints)

		// Grace Days (Duolingo streak freeze)
		gp.GET("/grace-days", h.GetGraceDays)
		gp.POST("/grace-days/purchase", h.PurchaseGraceDay)

		// Weekly Recap (Nike Run Club style)
		gp.GET("/recap", h.GetWeeklyRecap)
		gp.GET("/recap/history", h.GetWeeklyRecapHistory)

		// Test push notification — sends to the current user's devices
		gp.POST("/test-push", func(c *gin.Context) {
			userIDVal, _ := c.Get("user_id")
			userID, _ := userIDVal.(uuid.UUID)
			// Prefer the live push provider (OneSignal when configured, else Expo)
			var err error
			switch {
			case container.OneSignalPushService != nil:
				err = container.OneSignalPushService.SendToUser(c.Request.Context(), userID,
					"Rail Pro", "Push notifications are working!", map[string]interface{}{"type": "test"})
			case container.ExpoPushService != nil:
				err = container.ExpoPushService.SendToUser(c.Request.Context(), userID,
					"Rail Pro", "Push notifications are working!", map[string]interface{}{"type": "test"})
			default:
				c.JSON(500, gin.H{"error": "no push service configured"})
				return
			}
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Push sent"})
		})

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
