package routes

import (
	"github.com/gin-gonic/gin"

	securityHandlers "github.com/rail-service/rail_service/internal/api/handlers/security"
)

// RegisterSecurityFeatureRoutes registers the security feature endpoints
// Call this with the /security router group directly
func RegisterSecurityFeatureRoutes(rg *gin.RouterGroup, handler *securityHandlers.SecurityFeaturesHandler) {
	// Whitelist
	rg.POST("/whitelist", handler.AddWhitelistAddress)
	rg.GET("/whitelist", handler.GetWhitelistAddresses)
	rg.DELETE("/whitelist/:id", handler.RemoveWhitelistAddress)

	// Adaptive MFA
	rg.POST("/mfa/challenge", handler.RequestMFAChallenge)
	rg.POST("/mfa/verify-challenge", handler.VerifyMFAChallenge)
}
