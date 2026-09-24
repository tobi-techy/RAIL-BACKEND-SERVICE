package routes

import (
	"github.com/gin-gonic/gin"
	investmenthandlers "github.com/rail-service/rail_service/internal/api/handlers/investment"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
)

// RegisterInvestmentGliderRoutes registers the Glider-backed investment Agent
// API. These endpoints are what the Python MIRIAM agent (and the mobile app for
// confirmations) call; every mutation is validated, policy-checked and staged
// for explicit confirmation inside the service, so the routes only authenticate
// and delegate.
func RegisterInvestmentGliderRoutes(
	router *gin.RouterGroup,
	h *investmenthandlers.Handlers,
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

	investments := router.Group("/investments")
	investments.Use(middleware.Authentication(cfg, log, sessionValidator, tokenBlacklist))
	// Tokenized investing needs advanced (tier 3) verification. The policy
	// engine independently enforces the same rule; this gate turns it into a
	// clear 403 instead of a service error.
	investments.Use(middleware.RequireTokenizedInvestingCapability(userReader, log.Zap()))
	{
		// Capability and limits: answer "can I invest?" without acting.
		investments.GET("/limits", h.GetLimits)

		// Portfolio state. The agent reads this instead of reconstructing
		// portfolio values from memory.
		investments.GET("/portfolio", h.GetPortfolio)
		investments.GET("/positions", h.GetPositions)
		investments.GET("/owner", h.GetOwner)

		// Asset catalog.
		investments.GET("/assets", h.ListAssets)
		investments.GET("/assets/:id", h.GetAsset)

		// Discovery: public strategies as "investor" profiles, always carrying
		// provenance labels.
		investments.GET("/investors", h.ListInvestors)
		investments.GET("/investors/:id", h.GetInvestor)
		investments.GET("/investors/:id/activity", h.GetInvestorActivity)

		// Strategies: versioned and immutable.
		investments.GET("/strategies", h.ListStrategies)
		investments.POST("/strategies", h.CreateStrategy)
		// Static before param: Gin prefers the static "rail" segment over
		// ":id", so this never collides with GetStrategy.
		investments.GET("/strategies/rail", h.ListRailStrategies)
		investments.GET("/strategies/:id", h.GetStrategy)
		investments.PATCH("/strategies/:id", h.PatchStrategyMetadata)
		investments.GET("/strategies/:id/provider-versions", h.GetProviderStrategyVersions)
		investments.GET("/strategies/:id/performance", h.GetStrategyPerformance)
		investments.GET("/strategies/:id/schedule", h.GetStrategySchedule)
		investments.GET("/strategies/:id/preferences", h.GetStrategyPreferences)
		investments.GET("/strategies/:id/fees", h.GetStrategyFees)
		investments.POST("/strategies/:id/versions", h.PublishStrategyVersion)
		investments.GET("/strategies/:id/preview", h.PreviewRebalance)
		investments.POST("/strategies/:id/pause", h.PauseStrategy)
		investments.POST("/strategies/:id/resume", h.ResumeStrategy)
		investments.POST("/strategies/:id/rebalance", h.TriggerRebalance)

		// Enrollment reads backed by real Glider endpoints.
		investments.GET("/enrollments/:id/performance", h.GetEnrollmentPerformance)
		investments.GET("/enrollments/:id/sector-exposure", h.GetEnrollmentSectorExposure)
		investments.PATCH("/enrollments/:id", h.RenameEnrollment)
		investments.POST("/enrollments/:id/chains/signature", h.PrepareChainActivation)
		investments.POST("/breakdown", h.GetAllocationBreakdown)

		// Execution.
		investments.POST("/enroll", h.Enroll)
		investments.POST("/enroll/prepare", h.EnrollPrepare)
		investments.POST("/enroll/complete", h.EnrollComplete)
		investments.POST("/orders", h.PlaceOrder)
		investments.POST("/allocations", h.SetAllocation)

		// Audit trail of every investment action.
		investments.GET("/executions", h.ListExecutions)
		investments.GET("/executions/:id", h.GetExecution)
		investments.GET("/audit", h.ListAuditEvents)
	}

	// Money out. Two independent gates: a delegated agent token can never call
	// this (RequireInteractiveSession), and the request must carry a
	// passcode-verified session token, which only the app can obtain.
	withdrawals := investments.Group("")
	withdrawals.Use(middleware.RequireInteractiveSession())
	if passcodeValidator != nil {
		withdrawals.Use(middleware.RequirePasscodeSession(passcodeValidator, true, log.Zap()))
	}
	{
		withdrawals.POST("/withdrawals", h.Withdraw)
	}
}
