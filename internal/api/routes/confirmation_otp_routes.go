package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/rail-service/rail_service/internal/api/handlers/confirmation"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/platform/runtimereg"
)

func init() {
	OnEngine(func(router *gin.Engine) {
		mountConfirmationOTP(router)
	})
}

// RegisterConfirmationOTPRoutes mounts hackathon/demo email-OTP endpoints for
// money confirmation cards (CONFIRMATION_DEMO_EMAIL_OTP). Token-gated like
// Face ID approve; Face ID approve is rejected while the flag is on.
func RegisterConfirmationOTPRoutes(router *gin.Engine, container *di.Container) {
	if container != nil && container.ConfirmationHandlers != nil {
		registerConfirmationOTPHandlers(router, container.ConfirmationHandlers)
		return
	}
	mountConfirmationOTP(router)
}

func mountConfirmationOTP(router *gin.Engine) {
	v, ok := runtimereg.Get("confirmation_handlers")
	if !ok || v == nil {
		return
	}
	h, ok := v.(*confirmation.Handler)
	if !ok || h == nil {
		return
	}
	registerConfirmationOTPHandlers(router, h)
}

func registerConfirmationOTPHandlers(router *gin.Engine, h *confirmation.Handler) {
	if router == nil || h == nil {
		return
	}
	confirm := router.Group("/confirm")
	{
		confirm.POST("/:id/otp/send", middleware.RateLimit(5), h.SendOTP)
		confirm.POST("/:id/otp/approve", middleware.RateLimit(10), h.ApproveOTP)
	}
	router.POST("/api/v1/confirmations/:id/otp/send", middleware.RateLimit(5), h.SendOTP)
	router.POST("/api/v1/confirmations/:id/otp/approve", middleware.RateLimit(10), h.ApproveOTP)
}
