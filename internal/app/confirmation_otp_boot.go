package app

import (
	"github.com/gin-gonic/gin"

	"github.com/rail-service/rail_service/internal/api/routes"
)

// registerConfirmationOTP mounts hackathon email-OTP money confirmation routes.
// Call from initializeServer right after routes.SetupRoutes(app.container):
//
//	routes.RegisterConfirmationOTPRoutes(router, app.container)
//
// SetupSecurityRoutesEnhanced also mounts these via ApplyEngineHooks.
func (app *Application) registerConfirmationOTP(router *gin.Engine) {
	routes.RegisterConfirmationOTPRoutes(router, app.container)
}
