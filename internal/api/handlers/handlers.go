package handlers

import (
	"github.com/rail-service/rail_service/internal/api/handlers/admin"
	"github.com/rail-service/rail_service/internal/api/handlers/auth"
	"github.com/rail-service/rail_service/internal/api/handlers/cards"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/api/handlers/funding"
	"github.com/rail-service/rail_service/internal/api/handlers/investing"
	"github.com/rail-service/rail_service/internal/api/handlers/security"
	"github.com/rail-service/rail_service/internal/api/handlers/trading"
	"github.com/rail-service/rail_service/internal/api/handlers/wallet"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
)

type (
	// Auth
	AuthHandlers       = auth.AuthHandlers
	PasscodeHandlers   = auth.PasscodeHandlers
	SocialAuthHandlers = auth.SocialAuthHandlers
	MFAHandlers        = auth.MFAHandlers
	TwoFAHandlers      = auth.TwoFAHandlers
	SessionHandlers    = auth.SessionHandlers

	// Wallet
	WalletHandlers        = wallet.WalletHandlers
	WalletFundingHandlers = wallet.WalletFundingHandlers
	WithdrawalHandlers    = wallet.WithdrawalHandlers
	RecipientHandlers     = wallet.RecipientHandlers

	// Funding
	FundingHandlers = funding.FundingHandlers
	StationHandlers = funding.StationHandlers

	// Investing
	InvestingHandlers          = investing.InvestingHandlers
	AllocationHandlers         = investing.AllocationHandlers
	AnalyticsHandlers          = investing.AnalyticsHandlers
	AICfoHandler               = investing.AICfoHandler
	UsageHandlers              = investing.UsageHandlers
	KnowledgeHandlers          = investing.KnowledgeHandlers
	AutomationHandler          = investing.AutomationHandler
	FinancialObligationHandler = investing.FinancialObligationHandler
	MoneyGuardHandler          = investing.MoneyGuardHandler
	SpendingCommitmentHandler  = investing.SpendingCommitmentHandler
	FinancialSnapshotHandler   = investing.FinancialSnapshotHandler
	StatementUploadHandler     = investing.StatementUploadHandler

	// Trading
	CopyTradingHandlers         = trading.CopyTradingHandlers
	RebalancingHandlers         = trading.RebalancingHandlers
	ScheduledInvestmentHandlers = trading.ScheduledInvestmentHandlers

	// Cards
	CardHandlers    = cards.CardHandlers
	RoundupHandlers = cards.RoundupHandlers

	// Admin
	AdminHandlers            = admin.AdminHandlers
	SecurityAdminHandlers    = admin.SecurityAdminHandlers
	SecurityHandlers         = admin.SecurityHandlers         // Passcode-related security handlers
	EnhancedSecurityHandlers = admin.EnhancedSecurityHandlers // 2FA and session handlers

	// Webhooks
	WebhookHandlers      = webhooks.WebhookHandlers
	BridgeWebhookHandler = webhooks.BridgeWebhookHandler
	BridgeKYCHandlers    = webhooks.BridgeKYCHandlers

	// Security
	SecurityEnhancedHandlers = security.SecurityEnhancedHandlers
	APIKeyHandlers           = security.APIKeyHandlers
	LimitsHandler            = security.LimitsHandler

	// Common
	CoreHandlers   = common.CoreHandlers
	HealthHandler  = common.HealthHandler
	MobileHandlers = common.MobileHandlers
	// NotificationWorkerHandlers is deprecated — use WorkerAdminHandlers for worker admin
	// and NotificationHandlers for notification CRUD.
	NotificationWorkerHandlers = common.WorkerAdminHandlers
	WorkerAdminHandlers        = common.WorkerAdminHandlers
)

// Re-export constructors from subpackages

// Auth constructors
var (
	NewAuthHandlers       = auth.NewAuthHandlers
	NewPasscodeHandlers   = auth.NewPasscodeHandlers
	NewSocialAuthHandlers = auth.NewSocialAuthHandlers
	NewMFAHandlers        = auth.NewMFAHandlers
	NewTwoFAHandlers      = auth.NewTwoFAHandlers
	NewSessionHandlers    = auth.NewSessionHandlers
)

// Wallet constructors
var (
	NewWalletHandlers        = wallet.NewWalletHandlers
	NewWalletFundingHandlers = wallet.NewWalletFundingHandlers
	NewWithdrawalHandlers    = wallet.NewWithdrawalHandlers
	NewStashTransferHandlers = wallet.NewStashTransferHandlers
	NewRecipientHandlers     = wallet.NewRecipientHandlers
)

// Funding constructors
var (
	NewFundingHandlers = funding.NewFundingHandlers
	NewStationHandlers = funding.NewStationHandlers
)

// Investing constructors
var (
	NewInvestingHandlers           = investing.NewInvestingHandlers
	NewAllocationHandlers          = investing.NewAllocationHandlers
	NewAnalyticsHandlers           = investing.NewAnalyticsHandlers
	NewAICfoHandler                = investing.NewAICfoHandler
	NewUsageHandlers               = investing.NewUsageHandlers
	NewKnowledgeHandlers           = investing.NewKnowledgeHandlers
	NewReceiptSplitHandler         = investing.NewReceiptSplitHandler
	NewReceiptSplitTrackingHandler = investing.NewReceiptSplitTrackingHandler
	NewHouseholdHandler            = investing.NewHouseholdHandler
	NewAutomationHandler           = investing.NewAutomationHandler
	NewFinancialObligationHandler  = investing.NewFinancialObligationHandler
	NewMoneyGuardHandler           = investing.NewMoneyGuardHandler
	NewSpendingCommitmentHandler   = investing.NewSpendingCommitmentHandler
	NewFinancialSnapshotHandler    = investing.NewFinancialSnapshotHandler
	NewStatementUploadHandler      = investing.NewStatementUploadHandler
	NewStatementUploadHandlerV2    = investing.NewStatementUploadHandlerV2
	NewDocumentHandler             = investing.NewDocumentHandler
)

// Trading constructors
var (
	NewCopyTradingHandlers         = trading.NewCopyTradingHandlers
	NewRebalancingHandlers         = trading.NewRebalancingHandlers
	NewScheduledInvestmentHandlers = trading.NewScheduledInvestmentHandlers
)

// Cards constructors
var (
	NewCardHandlers    = cards.NewCardHandlers
	NewRoundupHandlers = cards.NewRoundupHandlers
)

// Admin constructors
var (
	NewAdminHandlers            = admin.NewAdminHandlers
	NewSecurityAdminHandlers    = admin.NewSecurityAdminHandlers
	NewSecurityHandlers         = admin.NewSecurityHandlers         // Passcode-related security handlers
	NewEnhancedSecurityHandlers = admin.NewEnhancedSecurityHandlers // 2FA and session handlers
)

// Webhooks constructors
var (
	NewWebhookHandlers      = webhooks.NewWebhookHandlers
	NewBridgeWebhookHandler = webhooks.NewBridgeWebhookHandler
	NewBridgeKYCHandlers    = webhooks.NewBridgeKYCHandlers
)

// Security constructors (device/IP/withdrawal security features)
var (
	NewSecurityEnhancedHandlers = security.NewSecurityEnhancedHandlers
	NewAPIKeyHandlers           = security.NewAPIKeyHandlers
	NewLimitsHandler            = security.NewLimitsHandler
)

// Common constructors
var (
	NewCoreHandlers               = common.NewCoreHandlers
	NewHealthHandler              = common.NewHealthHandler
	NewMobileHandlers             = common.NewMobileHandlers
	NewNotificationWorkerHandlers = common.NewWorkerAdminHandlers
	NewWorkerAdminHandlers        = common.NewWorkerAdminHandlers
)

// Re-export common utilities
var (
	RespondWithError   = common.RespondError
	RespondWithSuccess = common.RespondSuccess
	GetUserID          = common.GetUserID
	GetPagination      = common.ExtractPagination
)
