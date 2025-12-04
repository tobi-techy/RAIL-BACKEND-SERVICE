package balance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/integration"
	"github.com/rail-service/rail_service/pkg/logger"
)

// EnhancedService provides balance operations using the ledger system
// This replaces the legacy balance service with ledger-based operations
type EnhancedService struct {
	ledgerIntegration *integration.LedgerIntegration
	alpacaAdapter     AlpacaAdapter
	logger            *logger.Logger
}

// AlpacaAdapter interface for Alpaca API operations
type AlpacaAdapter interface {
	GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
}

// NewEnhancedService creates a new enhanced balance service
func NewEnhancedService(
	ledgerIntegration *integration.LedgerIntegration,
	alpacaAdapter AlpacaAdapter,
	logger *logger.Logger,
) *EnhancedService {
	return &EnhancedService{
		ledgerIntegration: ledgerIntegration,
		alpacaAdapter:     alpacaAdapter,
		logger:            logger,
	}
}

// GetBalance retrieves comprehensive user balance from ledger
func (s *EnhancedService) GetBalance(ctx context.Context, userID uuid.UUID) (*BalanceResponse, error) {
	s.logger.Info("Getting user balance from ledger", "user_id", userID)

	// Get balance from ledger
	ledgerBalance, err := s.ledgerIntegration.GetUserBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ledger balance: %w", err)
	}

	// Build response
	response := &BalanceResponse{
		UserID:            userID,
		USDCBalance:       ledgerBalance.USDCBalance,
		BuyingPower:       ledgerBalance.FiatExposure,
		PendingInvestment: ledgerBalance.PendingInvestment,
		TotalValue:        ledgerBalance.TotalValue,
		Currency:          "USD",

		// Detailed breakdown
		Breakdown: &BalanceBreakdown{
			AvailableUSDC:     ledgerBalance.USDCBalance,
			BuyingPowerUSD:    ledgerBalance.FiatExposure,
			PendingInvestment: ledgerBalance.PendingInvestment,
			TotalLiquidValue:  ledgerBalance.TotalValue,
		},
	}

	s.logger.Info("Balance retrieved from ledger",
		"user_id", userID,
		"total_value", ledgerBalance.TotalValue,
		"usdc_balance", ledgerBalance.USDCBalance,
		"buying_power", ledgerBalance.FiatExposure)

	return response, nil
}

// GetDetailedBalance retrieves balance with Alpaca positions
func (s *EnhancedService) GetDetailedBalance(
	ctx context.Context,
	userID uuid.UUID,
	alpacaAccountID string,
) (*DetailedBalanceResponse, error) {
	// Get base balance from ledger
	baseBalance, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Get Alpaca account details for positions
	var alpacaEquity decimal.Decimal
	var positionsValue decimal.Decimal

	if alpacaAccountID != "" {
		alpacaResp, err := s.alpacaAdapter.GetAccountBalance(ctx, alpacaAccountID)
		if err != nil {
			s.logger.Error("Failed to get Alpaca balance",
				"user_id", userID,
				"alpaca_account_id", alpacaAccountID,
				"error", err)
			// Don't fail, just use ledger data
		} else {
			alpacaEquity = alpacaResp.Equity
			positionsValue = alpacaResp.PortfolioValue
		}
	}

	// Build detailed response
	detailed := &DetailedBalanceResponse{
		UserID:              userID,
		USDCBalance:         baseBalance.USDCBalance,
		BuyingPower:         baseBalance.BuyingPower,
		PendingInvestment:   baseBalance.PendingInvestment,
		InvestedValue:       positionsValue,
		TotalPortfolioValue: baseBalance.TotalValue.Add(positionsValue),
		Currency:            "USD",

		AlpacaAccountDetails: &AlpacaAccountDetails{
			AccountID:      alpacaAccountID,
			Equity:         alpacaEquity,
			PositionsValue: positionsValue,
		},
	}

	return detailed, nil
}

// CheckAvailableBalance checks if user has sufficient balance for operation
func (s *EnhancedService) CheckAvailableBalance(
	ctx context.Context,
	userID uuid.UUID,
	requiredAmount decimal.Decimal,
	accountType entities.AccountType,
) (bool, error) {
	balance, err := s.ledgerIntegration.GetUserBalance(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to get balance: %w", err)
	}

	var available decimal.Decimal
	switch accountType {
	case entities.AccountTypeUSDCBalance:
		available = balance.USDCBalance
	case entities.AccountTypeFiatExposure:
		available = balance.FiatExposure
	case entities.AccountTypePendingInvestment:
		available = balance.PendingInvestment
	default:
		return false, fmt.Errorf("unsupported account type: %s", accountType)
	}

	hasBalance := available.GreaterThanOrEqual(requiredAmount)

	s.logger.Info("Balance check",
		"user_id", userID,
		"account_type", accountType,
		"required", requiredAmount,
		"available", available,
		"sufficient", hasBalance)

	return hasBalance, nil
}

// Response types

// BalanceResponse represents a user's balance
type BalanceResponse struct {
	UserID            uuid.UUID         `json:"user_id"`
	USDCBalance       decimal.Decimal   `json:"usdc_balance"`
	BuyingPower       decimal.Decimal   `json:"buying_power"`
	PendingInvestment decimal.Decimal   `json:"pending_investment"`
	TotalValue        decimal.Decimal   `json:"total_value"`
	Currency          string            `json:"currency"`
	Breakdown         *BalanceBreakdown `json:"breakdown,omitempty"`
}

// BalanceBreakdown provides detailed balance breakdown
type BalanceBreakdown struct {
	AvailableUSDC     decimal.Decimal `json:"available_usdc"`
	BuyingPowerUSD    decimal.Decimal `json:"buying_power_usd"`
	PendingInvestment decimal.Decimal `json:"pending_investment"`
	TotalLiquidValue  decimal.Decimal `json:"total_liquid_value"`
}

// DetailedBalanceResponse includes positions and Alpaca details
type DetailedBalanceResponse struct {
	UserID               uuid.UUID             `json:"user_id"`
	USDCBalance          decimal.Decimal       `json:"usdc_balance"`
	BuyingPower          decimal.Decimal       `json:"buying_power"`
	PendingInvestment    decimal.Decimal       `json:"pending_investment"`
	InvestedValue        decimal.Decimal       `json:"invested_value"`
	TotalPortfolioValue  decimal.Decimal       `json:"total_portfolio_value"`
	Currency             string                `json:"currency"`
	AlpacaAccountDetails *AlpacaAccountDetails `json:"alpaca_details,omitempty"`
}

// AlpacaAccountDetails contains Alpaca account information
type AlpacaAccountDetails struct {
	AccountID      string          `json:"account_id"`
	Equity         decimal.Decimal `json:"equity"`
	PositionsValue decimal.Decimal `json:"positions_value"`
}
