package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
)

// RegisterConfirmationOTPRoutes mounts hackathon/demo email-OTP endpoints for
// money confirmation cards (CONFIRMATION_DEMO_EMAIL_OTP). Token-gated like
// Face ID approve; Face ID approve is rejected while the flag is on.
func RegisterConfirmationOTPRoutes(router *gin.Engine, container *di.Container) {
	if container == nil || container.ConfirmationHandlers == nil {
		return
	}
	confirm := router.Group("/confirm")
	{
		confirm.POST("/:id/otp/send", middleware.RateLimit(5), container.ConfirmationHandlers.SendOTP)
		confirm.POST("/:id/otp/approve", middleware.RateLimit(10), container.ConfirmationHandlers.ApproveOTP)
	}
	router.POST("/api/v1/confirmations/:id/otp/send",
		middleware.RateLimit(5),
		container.ConfirmationHandlers.SendOTP)
	router.POST("/api/v1/confirmations/:id/otp/approve",
		middleware.RateLimit(10),
		container.ConfirmationHandlers.ApproveOTP)
}
