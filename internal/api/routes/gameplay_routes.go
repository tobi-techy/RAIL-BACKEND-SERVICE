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

	gp := rg.Group("/gameplay")
	{
		gp.GET("/profile", h.GetProfile)
		gp.GET("/streaks", h.GetStreaks)
		gp.GET("/activity-heatmap", h.GetActivityHeatmap)
		gp.GET("/xp", h.GetXP)
		gp.GET("/xp/history", h.GetXPHistory)
		gp.GET("/challenges", h.GetChallenges)
		gp.GET("/achievements", h.GetAchievements)

		// Test push notification — sends to the current user's devices
		gp.POST("/test-push", func(c *gin.Context) {
			userIDVal, _ := c.Get("user_id")
			userID, _ := userIDVal.(uuid.UUID)
			// Clear stale endpoint ARNs so SNS creates fresh ones on the current platform
			if container.DeviceTokenRepo != nil {
				tokens, _ := container.DeviceTokenRepo.GetUserTokens(c.Request.Context(), userID)
				for _, t := range tokens {
					if t.EndpointARN != nil && *t.EndpointARN != "" {
						container.DeviceTokenRepo.UpdateEndpointARN(c.Request.Context(), t.ID, "")
					}
				}
			}
			// Try SNS first, fall back to Expo
			var err error
			if container.SNSPushService != nil {
				err = container.SNSPushService.SendToUser(c.Request.Context(), userID,
					"Rail Pro", "Push notifications are working!", map[string]interface{}{"type": "test"})
			} else if container.ExpoPushService != nil {
				err = container.ExpoPushService.SendToUser(c.Request.Context(), userID,
					"Rail Pro", "Push notifications are working!", map[string]interface{}{"type": "test"})
			} else {
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
