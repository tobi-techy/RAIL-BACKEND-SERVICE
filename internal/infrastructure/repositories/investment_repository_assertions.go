package repositories

import "github.com/rail-service/rail_service/internal/domain/services/investment"

// Compile-time proof that the PostgreSQL repositories satisfy the investment
// domain ports. If a method signature drifts, this file stops building.
var (
	_ investment.StrategyRepository         = (*InvestmentStrategyRepository)(nil)
	_ investment.AssetRepository            = (*InvestmentAssetRepository)(nil)
	_ investment.EnrollmentRepository       = (*InvestmentEnrollmentRepository)(nil)
	_ investment.HoldingRepository          = (*InvestmentHoldingRepository)(nil)
	_ investment.ExecutionRepository        = (*InvestmentExecutionRepository)(nil)
	_ investment.FundingTransferRepository  = (*InvestmentFundingTransferRepository)(nil)
	_ investment.SignatureRequestRepository = (*InvestmentSignatureRequestRepository)(nil)
	_ investment.ConfirmationRepository     = (*InvestmentConfirmationRepository)(nil)
	_ investment.OperationRepository        = (*InvestmentOperationRepository)(nil)
	_ investment.AuditRepository            = (*InvestmentAuditRepository)(nil)
	_ investment.LimitsRepository           = (*InvestmentLimitsRepository)(nil)
	_ investment.UserProfileReader          = (*InvestmentUserProfileReader)(nil)
)
