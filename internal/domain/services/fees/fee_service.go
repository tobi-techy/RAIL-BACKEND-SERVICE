package fees

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles fee calculations and transparency
type Service struct {
	feeConfigRepo FeeConfigRepository
	logger        *logger.Logger
}

// FeeConfigRepository interface for fee configuration
type FeeConfigRepository interface {
	GetFeeConfig(ctx context.Context, operationType string) (*FeeConfig, error)
}

// FeeConfig represents fee configuration for an operation
type FeeConfig struct {
	OperationType  string          `json:"operation_type"`
	BaseFee        decimal.Decimal `json:"base_fee"`
	PercentageFee  decimal.Decimal `json:"percentage_fee"`
	MinFee         decimal.Decimal `json:"min_fee"`
	MaxFee         decimal.Decimal `json:"max_fee"`
	Currency       string          `json:"currency"`
	EffectiveFrom  time.Time       `json:"effective_from"`
	EffectiveUntil *time.Time      `json:"effective_until,omitempty"`
	IsActive       bool            `json:"is_active"`
	Description    string          `json:"description"`
}

// FeeBreakdown represents a detailed fee breakdown
type FeeBreakdown struct {
	OperationType   string          `json:"operationType"`
	OperationAmount decimal.Decimal `json:"operationAmount"`
	BaseFee         decimal.Decimal `json:"baseFee"`
	PercentageFee   decimal.Decimal `json:"percentageFee"`
	NetworkFee      decimal.Decimal `json:"networkFee"`
	TotalFee        decimal.Decimal `json:"totalFee"`
	Currency        string          `json:"currency"`
	FeeDescription  string          `json:"feeDescription"`
	Transparent     bool            `json:"transparent"`
}

// FeeEstimate represents a fee estimate for an operation
type FeeEstimate struct {
	OperationType   string           `json:"operationType"`
	EstimatedAmount decimal.Decimal  `json:"estimatedAmount"`
	FeeBreakdown    *FeeBreakdown    `json:"feeBreakdown"`
	ExchangeRate    *decimal.Decimal `json:"exchangeRate,omitempty"`
	FinalAmount     decimal.Decimal  `json:"finalAmount"`
	Currency        string           `json:"currency"`
	ExpiresAt       time.Time        `json:"expiresAt"`
}

// FeeSummary represents a summary of fees for a period
type FeeSummary struct {
	UserID         string                     `json:"userId"`
	Period         string                     `json:"period"` // daily, weekly, monthly
	StartDate      time.Time                  `json:"startDate"`
	EndDate        time.Time                  `json:"endDate"`
	TotalFeesPaid  decimal.Decimal            `json:"totalFeesPaid"`
	FeesByType     map[string]decimal.Decimal `json:"feesByType"`
	OperationCount int                        `json:"operationCount"`
	Currency       string                     `json:"currency"`
}

// NewService creates a new fee service
func NewService(feeConfigRepo FeeConfigRepository, logger *logger.Logger) *Service {
	return &Service{
		feeConfigRepo: feeConfigRepo,
		logger:        logger,
	}
}

// CalculateFee calculates the fee for a given operation
func (s *Service) CalculateFee(ctx context.Context, operationType string, amount decimal.Decimal, networkFee decimal.Decimal) (*FeeBreakdown, error) {
	// Get fee configuration
	config, err := s.feeConfigRepo.GetFeeConfig(ctx, operationType)
	if err != nil {
		return nil, fmt.Errorf("failed to get fee config for %s: %w", operationType, err)
	}

	if !config.IsActive {
		return nil, fmt.Errorf("fee configuration for %s is not active", operationType)
	}

	// Calculate percentage fee
	percentageFee := amount.Mul(config.PercentageFee)

	// Calculate total fee before min/max caps
	totalFee := config.BaseFee.Add(percentageFee).Add(networkFee)

	// Apply min/max constraints
	if totalFee.LessThan(config.MinFee) {
		totalFee = config.MinFee
	}
	if totalFee.GreaterThan(config.MaxFee) {
		totalFee = config.MaxFee
	}

	breakdown := &FeeBreakdown{
		OperationType:   operationType,
		OperationAmount: amount,
		BaseFee:         config.BaseFee,
		PercentageFee:   percentageFee,
		NetworkFee:      networkFee,
		TotalFee:        totalFee,
		Currency:        config.Currency,
		FeeDescription:  config.Description,
		Transparent:     true,
	}

	s.logger.Debugw("Fee calculated",
		"operation_type", operationType,
		"amount", amount.String(),
		"total_fee", totalFee.String(),
	)

	return breakdown, nil
}

// EstimateFee provides a fee estimate for an operation
func (s *Service) EstimateFee(ctx context.Context, operationType string, amount decimal.Decimal) (*FeeEstimate, error) {
	// Estimate network fee (simplified - in production, get from blockchain)
	estimatedNetworkFee := s.estimateNetworkFee(operationType, amount)

	breakdown, err := s.CalculateFee(ctx, operationType, amount, estimatedNetworkFee)
	if err != nil {
		return nil, err
	}

	finalAmount := amount.Add(breakdown.TotalFee)

	estimate := &FeeEstimate{
		OperationType:   operationType,
		EstimatedAmount: amount,
		FeeBreakdown:    breakdown,
		FinalAmount:     finalAmount,
		Currency:        breakdown.Currency,
		ExpiresAt:       time.Now().Add(5 * time.Minute), // Estimate valid for 5 minutes
	}

	return estimate, nil
}

// GetFeeBreakdown provides detailed fee information for an operation type
func (s *Service) GetFeeBreakdown(ctx context.Context, operationType string) (*FeeConfig, error) {
	return s.feeConfigRepo.GetFeeConfig(ctx, operationType)
}

// ValidateFeeAmount checks if a fee amount is reasonable for the operation
func (s *Service) ValidateFeeAmount(ctx context.Context, operationType string, amount, feeAmount decimal.Decimal) error {
	breakdown, err := s.CalculateFee(ctx, operationType, amount, decimal.Zero)
	if err != nil {
		return err
	}

	// Allow 10% variance from calculated fee
	minAcceptable := breakdown.TotalFee.Mul(decimal.NewFromFloat(0.9))
	maxAcceptable := breakdown.TotalFee.Mul(decimal.NewFromFloat(1.1))

	if feeAmount.LessThan(minAcceptable) {
		return fmt.Errorf("fee amount too low: expected at least %s, got %s", minAcceptable.String(), feeAmount.String())
	}
	if feeAmount.GreaterThan(maxAcceptable) {
		return fmt.Errorf("fee amount too high: expected at most %s, got %s", maxAcceptable.String(), feeAmount.String())
	}

	return nil
}

// GetTransparentFeeSchedule returns all fee configurations for transparency
func (s *Service) GetTransparentFeeSchedule(ctx context.Context) (map[string]*FeeConfig, error) {
	// This would typically query all active fee configurations
	// For now, return hardcoded examples
	schedule := map[string]*FeeConfig{
		"deposit": {
			OperationType: "deposit",
			BaseFee:       decimal.NewFromFloat(0.0),
			PercentageFee: decimal.NewFromFloat(0.001), // 0.1%
			MinFee:        decimal.NewFromFloat(0.01),
			MaxFee:        decimal.NewFromFloat(5.0),
			Currency:      "USD",
			IsActive:      true,
			Description:   "Deposit fee covers blockchain network costs",
		},
		"withdrawal": {
			OperationType: "withdrawal",
			BaseFee:       decimal.NewFromFloat(0.25),
			PercentageFee: decimal.NewFromFloat(0.005), // 0.5%
			MinFee:        decimal.NewFromFloat(0.50),
			MaxFee:        decimal.NewFromFloat(10.0),
			Currency:      "USD",
			IsActive:      true,
			Description:   "Withdrawal fee covers compliance, network, and processing costs",
		},
		"trading": {
			OperationType: "trading",
			BaseFee:       decimal.NewFromFloat(0.0),
			PercentageFee: decimal.NewFromFloat(0.0025), // 0.25%
			MinFee:        decimal.NewFromFloat(0.01),
			MaxFee:        decimal.NewFromFloat(25.0),
			Currency:      "USD",
			IsActive:      true,
			Description:   "Trading fee covers market data and execution costs",
		},
		"conversion": {
			OperationType: "conversion",
			BaseFee:       decimal.NewFromFloat(0.10),
			PercentageFee: decimal.NewFromFloat(0.001), // 0.1%
			MinFee:        decimal.NewFromFloat(0.10),
			MaxFee:        decimal.NewFromFloat(2.0),
			Currency:      "USD",
			IsActive:      true,
			Description:   "Conversion fee covers currency exchange and processing",
		},
	}

	return schedule, nil
}

// estimateNetworkFee provides a rough estimate of network fees
func (s *Service) estimateNetworkFee(operationType string, amount decimal.Decimal) decimal.Decimal {
	// Simplified network fee estimation
	// In production, this would query current network conditions

	switch operationType {
	case "withdrawal":
		// Base network fee for blockchain transfers
		if amount.LessThan(decimal.NewFromInt(100)) {
			return decimal.NewFromFloat(0.50)
		} else if amount.LessThan(decimal.NewFromInt(1000)) {
			return decimal.NewFromFloat(1.00)
		} else {
			return decimal.NewFromFloat(2.00)
		}
	case "deposit":
		// Deposits typically have lower fees
		return decimal.NewFromFloat(0.10)
	case "trading":
		// Trading fees are usually covered by the broker
		return decimal.Zero
	default:
		return decimal.NewFromFloat(0.25)
	}
}

// CalculateTotalWithFees calculates the total amount including fees
func (s *Service) CalculateTotalWithFees(ctx context.Context, operationType string, amount decimal.Decimal) (decimal.Decimal, *FeeBreakdown, error) {
	breakdown, err := s.CalculateFee(ctx, operationType, amount, s.estimateNetworkFee(operationType, amount))
	if err != nil {
		return decimal.Zero, nil, err
	}

	total := amount.Add(breakdown.TotalFee)
	return total, breakdown, nil
}

// FormatFeeDisplay formats fees for user display
func (s *Service) FormatFeeDisplay(breakdown *FeeBreakdown) string {
	if breakdown.TotalFee.IsZero() {
		return "No fees for this operation"
	}

	return fmt.Sprintf("Total fee: %s %s (%s base + %s percentage + %s network)",
		breakdown.TotalFee.String(),
		breakdown.Currency,
		breakdown.BaseFee.String(),
		breakdown.PercentageFee.String(),
		breakdown.NetworkFee.String(),
	)
}

// GetFeeDisclaimer returns a standard fee disclaimer
func (s *Service) GetFeeDisclaimer() string {
	return "Fees are subject to change and may vary based on network conditions, operation size, and market volatility. " +
		"Always review fee estimates before confirming transactions. " +
		"STACK is committed to transparent fee disclosure and competitive pricing."
}

// ValidateFeeStructure validates that fee configurations are reasonable
func (s *Service) ValidateFeeStructure(config *FeeConfig) error {
	if config.BaseFee.IsNegative() {
		return fmt.Errorf("base fee cannot be negative")
	}
	if config.PercentageFee.IsNegative() || config.PercentageFee.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("percentage fee must be between 0 and 1")
	}
	if config.MinFee.IsNegative() {
		return fmt.Errorf("minimum fee cannot be negative")
	}
	if config.MaxFee.IsNegative() {
		return fmt.Errorf("maximum fee cannot be negative")
	}
	if config.MinFee.GreaterThan(config.MaxFee) {
		return fmt.Errorf("minimum fee cannot be greater than maximum fee")
	}
	return nil
}
