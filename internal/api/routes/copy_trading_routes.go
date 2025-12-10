package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers"
)

// SetupCopyTradingRoutes configures copy trading API routes
func SetupCopyTradingRoutes(rg *gin.RouterGroup, copyTradingHandlers *handlers.CopyTradingHandlers, authMiddleware gin.HandlerFunc) {
	copy := rg.Group("/copy")
	{
		// Public routes - view conductors (requires auth but no special permissions)
		copy.Use(authMiddleware)
		
		// Conductor routes
		conductors := copy.Group("/conductors")
		{
			conductors.GET("", copyTradingHandlers.ListConductors)
			conductors.GET("/:id", copyTradingHandlers.GetConductor)
			conductors.GET("/:id/signals", copyTradingHandlers.GetConductorSignals)
		}

		// Draft routes (user's copy relationships)
		drafts := copy.Group("/drafts")
		{
			drafts.GET("", copyTradingHandlers.ListUserDrafts)
			drafts.POST("", copyTradingHandlers.CreateDraft)
			drafts.GET("/:id", copyTradingHandlers.GetDraft)
			drafts.DELETE("/:id", copyTradingHandlers.UnlinkDraft)
			drafts.POST("/:id/pause", copyTradingHandlers.PauseDraft)
			drafts.POST("/:id/resume", copyTradingHandlers.ResumeDraft)
			drafts.PUT("/:id/resize", copyTradingHandlers.ResizeDraft)
			drafts.GET("/:id/history", copyTradingHandlers.GetDraftHistory)
		}
	}
}
