package routes

import (
	"github.com/gin-gonic/gin"
	monohandlers "github.com/rail-service/rail_service/internal/api/handlers/mono"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	"go.uber.org/zap"
)

// RegisterMonoRoutes registers Mono open-banking routes under the protected group.
//
//	POST   /mono/link/initiate              — start Mono Connect widget
//	POST   /mono/link/complete              — exchange widget code for account ID
//	GET    /mono/accounts                   — list linked accounts
//	POST   /mono/accounts/:id/sync          — sync transactions from Mono
//	GET    /mono/accounts/:id/transactions  — get imported transactions
//	DELETE /mono/accounts/:id               — unlink account
//	GET    /mono/analysis                   — spending analysis by category
//	POST   /mono/deposit/initiate           — initiate DirectPay one-time debit
//	GET    /mono/deposit/:reference/verify  — verify payment status
func RegisterMonoRoutes(protected *gin.RouterGroup, service *monosvc.Service, logger *zap.Logger) {
	h := monohandlers.NewHandlers(service, logger)
	mono := protected.Group("/mono")
	{
		mono.POST("/link/initiate", h.InitiateLink)
		mono.POST("/link/complete", h.CompleteLink)
		mono.GET("/accounts", h.ListAccounts)
		mono.POST("/accounts/:id/sync", h.SyncAccount)
		mono.GET("/accounts/:id/transactions", h.GetTransactions)
		mono.DELETE("/accounts/:id", h.UnlinkAccount)
		mono.GET("/analysis", h.GetAnalysis)
		mono.POST("/deposit/initiate", h.InitiateDeposit)
		mono.GET("/deposit/:reference/verify", h.VerifyDeposit)
	}
}
