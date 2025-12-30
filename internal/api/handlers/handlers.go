// Package handlers provides HTTP request handlers for the Rail API.
// This file re-exports handler constructors from subpackages for backward compatibility.
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

// Re-export types from subpackages
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
	InvestmentHandlers        = investing.InvestmentHandlers
	InvestingHandlers         = investing.InvestingHandlers
	AllocationHandlers        = investing.AllocationHandlers
	PortfolioActivityHandlers = investing.PortfolioActivityHandlers
	AnalyticsHandlers         = investing.AnalyticsHandlers
	MarketHandlers            = investing.MarketHandlers
	AIChatHandlers            = investing.AIChatHandlers
	AICfoHandler              = investing.AICfoHandler

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
	SecurityHandlers         = admin.SecurityHandlers          // Passcode-related security handlers
	EnhancedSecurityHandlers = admin.EnhancedSecurityHandlers  // 2FA and session handlers

	// Webhooks
	WebhookHandlers        = webhooks.WebhookHandlers
	BridgeWebhookHandler   = webhooks.BridgeWebhookHandler
	CircleWebhookHandler   = webhooks.CircleWebhookHandler
	AlpacaWebhookHandlers  = webhooks.AlpacaWebhookHandlers
	BridgeKYCHandlers      = webhooks.BridgeKYCHandlers

	// Security
	SecurityEnhancedHandlers = security.SecurityEnhancedHandlers
	APIKeyHandlers           = security.APIKeyHandlers
	LimitsHandler            = security.LimitsHandler

	// Common
	CoreHandlers               = common.CoreHandlers
	HealthHandler              = common.HealthHandler
	IntegrationHandlers        = common.IntegrationHandlers
	MobileHandlers             = common.MobileHandlers
	NewsHandlers               = common.NewsHandlers
	NotificationWorkerHandlers = common.NotificationWorkerHandlers
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
	NewRecipientHandlers     = wallet.NewRecipientHandlers
)

// Funding constructors
var (
	NewFundingHandlers = funding.NewFundingHandlers
	NewStationHandlers = funding.NewStationHandlers
)

// Investing constructors
var (
	NewInvestmentHandlers        = investing.NewInvestmentHandlers
	NewInvestingHandlers         = investing.NewInvestingHandlers
	NewAllocationHandlers        = investing.NewAllocationHandlers
	NewPortfolioActivityHandlers = investing.NewPortfolioActivityHandlers
	NewAnalyticsHandlers         = investing.NewAnalyticsHandlers
	NewMarketHandlers            = investing.NewMarketHandlers
	NewAIChatHandlers            = investing.NewAIChatHandlers
	NewAICfoHandler              = investing.NewAICfoHandler
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
	NewSecurityHandlers         = admin.NewSecurityHandlers          // Passcode-related security handlers
	NewEnhancedSecurityHandlers = admin.NewEnhancedSecurityHandlers  // 2FA and session handlers
)

// Webhooks constructors
var (
	NewWebhookHandlers        = webhooks.NewWebhookHandlers
	NewBridgeWebhookHandler   = webhooks.NewBridgeWebhookHandler
	NewCircleWebhookHandler   = webhooks.NewCircleWebhookHandler
	NewAlpacaWebhookHandlers  = webhooks.NewAlpacaWebhookHandlers
	NewBridgeKYCHandlers      = webhooks.NewBridgeKYCHandlers
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
	NewIntegrationHandlers        = common.NewIntegrationHandlers
	NewMobileHandlers             = common.NewMobileHandlers
	NewNewsHandlers               = common.NewNewsHandlers
	NewNotificationWorkerHandlers = common.NewNotificationWorkerHandlers
)

// Re-export common utilities
var (
	RespondWithError   = common.RespondError
	RespondWithSuccess = common.RespondSuccess
	GetUserID          = common.GetUserID
	GetPagination      = common.ExtractPagination
)

// Re-export interfaces from investing package
type (
	InvestmentStreakRepository   = investing.InvestmentStreakRepository
	UserContributionsRepository  = investing.UserContributionsRepository
)
