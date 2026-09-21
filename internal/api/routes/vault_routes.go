package routes

import (
	"github.com/gin-gonic/gin"
	vaulthandlers "github.com/rail-service/rail_service/internal/api/handlers/vault"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterVaultRoutes registers the retirement vault API.
//
// Reads and setup are ordinary authenticated calls. Money out carries two
// independent gates on top of authentication: a delegated agent token can never
// call it (RequireInteractiveSession) and the request must carry a
// passcode-verified session, which only the app can obtain.
func RegisterVaultRoutes(
	router *gin.RouterGroup,
	h *vaulthandlers.Handlers,
	cfg *config.Config,
	log *logger.Logger,
	sessionValidator middleware.SessionValidator,
	tokenBlacklist *auth.TokenBlacklist,
	userReader middleware.UserEntityReader,
	passcodeValidator middleware.PasscodeSessionValidator,
) {
	if h == nil {
		return
	}

	vault := router.Group("/vault")
	vault.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
	vault.Use(middleware.RequireTokenizedInvestingCapability(userReader, log.Zap()))
	{
		vault.GET("", h.GetVault)
		vault.POST("", h.CreateVault)
		vault.PATCH("", h.UpdateVault)
		vault.GET("/strategies", h.ListStrategies)
		vault.GET("/activity", h.Activity)
	}

	// Withdrawal preview needs an interactive session too: it exposes the split
	// of a specific amount, which a messaging channel should not be able to
	// enumerate.
	withdrawals := vault.Group("")
	withdrawals.Use(middleware.RequireInteractiveSession())
	if passcodeValidator != nil {
		withdrawals.Use(middleware.RequirePasscodeSession(passcodeValidator, true, log.Zap()))
	}
	{
		withdrawals.POST("/withdraw/preview", h.PreviewWithdrawal)
		withdrawals.POST("/withdraw", h.Withdraw)
	}
}
