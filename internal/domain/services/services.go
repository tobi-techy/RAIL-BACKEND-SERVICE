// Package services provides domain services for the Rail application.
// This file re-exports service types and constructors from subpackages for backward compatibility.
package services

import (
	"github.com/rail-service/rail_service/internal/domain/services/alpaca"
	"github.com/rail-service/rail_service/internal/domain/services/balance"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/notification"
	"github.com/rail-service/rail_service/internal/domain/services/offramp"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/transaction"
	"github.com/rail-service/rail_service/internal/domain/services/verification"
	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
)

// Re-export types from subpackages for backward compatibility

// Withdrawal types
type (
	WithdrawalService             = withdrawal.WithdrawalService
	AlpacaAdapter                 = withdrawal.AlpacaAdapter
	WithdrawalProviderAdapter     = withdrawal.WithdrawalProviderAdapter
	WithdrawalNotificationService = withdrawal.WithdrawalNotificationService
	ProcessWithdrawalResponse     = withdrawal.ProcessWithdrawalResponse
	OnRampTransferResponse        = withdrawal.OnRampTransferResponse
)

// Notification types
type NotificationService = notification.NotificationService

// Verification types
type VerificationService = verification.VerificationService

// Balance types
type BalanceService = balance.BalanceService

// Onboarding types
type OnboardingJobService = onboarding.OnboardingJobService

// Offramp types
type OffRampService = offramp.OffRampService

// Transaction types
type TransactionControlService = transaction.TransactionControlService

// Investing types
type (
	BasketExecutor   = investing.BasketExecutor
	PortfolioService = investing.PortfolioService
)

// Alpaca types
type BrokerageOnboardingService = alpaca.BrokerageOnboardingService

// Funding types
type InstantFundingService = funding.InstantFundingService

// Re-export constructors
var (
	NewWithdrawalService          = withdrawal.NewWithdrawalService
	NewNotificationService        = notification.NewNotificationService
	NewVerificationService        = verification.NewVerificationService
	NewBalanceService             = balance.NewBalanceService
	NewOnboardingJobService       = onboarding.NewOnboardingJobService
	NewOffRampService             = offramp.NewOffRampService
	NewTransactionControlService  = transaction.NewTransactionControlService
	NewBasketExecutor             = investing.NewBasketExecutor
	NewPortfolioService           = investing.NewPortfolioService
	NewBrokerageOnboardingService = alpaca.NewBrokerageOnboardingService
	NewInstantFundingService      = funding.NewInstantFundingService
)
