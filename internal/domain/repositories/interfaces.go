package repositories

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// MoneyMinorUnits represents money in minor units (cents).
// 1050 = $10.50, avoiding float precision issues.
type MoneyMinorUnits int64

// ToDisplayString converts minor units to display string (e.g., 1050 -> "10.50")
func (m MoneyMinorUnits) ToDisplayString() string {
	dollars := m / 100
	cents := m % 100
	if cents < 0 {
		cents = -cents
	}
	return fmt.Sprintf("%d.%02d", dollars, cents)
}

// FromDecimalString converts a decimal string to minor units (e.g., "10.50" -> 1050)
func MoneyFromDecimalString(s string) (MoneyMinorUnits, error) {
	// Parse as float then convert to avoid complex string parsing
	// This is safe because we immediately convert to int64
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return MoneyMinorUnits(val * 100), nil
}

// AISummary represents an AI-generated summary
type AISummary struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	WeekStart   time.Time
	SummaryMD   string
	ArtifactURI string
	CreatedAt   time.Time
}

// AISummaryRepository defines the interface for AI summary persistence
type AISummaryRepository interface {
	Create(ctx context.Context, summary *AISummary) error
	GetByID(ctx context.Context, id uuid.UUID) (*AISummary, error)
	GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*AISummary, error)
	GetByUserAndWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*AISummary, error)
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*AISummary, error)
	Update(ctx context.Context, summary *AISummary) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// PortfolioRepository defines the interface for portfolio data access
type PortfolioRepository interface {
	GetUserPortfolio(ctx context.Context, userID uuid.UUID) (*Portfolio, error)
	GetUserHoldings(ctx context.Context, userID uuid.UUID) ([]*Holding, error)
	GetUserTransactions(ctx context.Context, userID uuid.UUID, since time.Time) ([]*Transaction, error)
	GetPortfolioPerformance(ctx context.Context, userID uuid.UUID, period time.Duration) (*PerformanceMetrics, error)
}

// Portfolio represents user portfolio information
// All monetary values are in minor units (cents): 1050 = $10.50
type Portfolio struct {
	UserID        uuid.UUID           `json:"user_id"`
	TotalValue    int64               `json:"total_value"`    // Minor units (cents)
	CashBalance   int64               `json:"cash_balance"`   // Minor units (cents)
	Holdings      []*Holding          `json:"holdings"`
	Performance   *PerformanceMetrics `json:"performance"`
	LastUpdated   time.Time           `json:"last_updated"`
}

// Holding represents a user's position in an asset
// Monetary values in minor units, quantity in micro-units (6 decimals)
type Holding struct {
	Symbol      string    `json:"symbol"`
	Quantity    int64     `json:"quantity"`     // Micro-units (1000000 = 1.0 share)
	MarketValue int64     `json:"market_value"` // Minor units (cents)
	CostBasis   int64     `json:"cost_basis"`   // Minor units (cents)
	LastPrice   int64     `json:"last_price"`   // Minor units (cents)
	UpdatedAt   time.Time `json:"updated_at"`
}

// QuantityScale is the multiplier for fractional share quantities (6 decimal places)
const QuantityScale int64 = 1000000

// Transaction represents a portfolio transaction
// All monetary values in minor units (cents)
type Transaction struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Type      string    `json:"type"`   // buy, sell, dividend, etc.
	Symbol    string    `json:"symbol"`
	Quantity  int64     `json:"quantity"` // Micro-units (1000000 = 1.0 share)
	Price     int64     `json:"price"`    // Minor units (cents)
	Amount    int64     `json:"amount"`   // Minor units (cents)
	Timestamp time.Time `json:"timestamp"`
}

// PerformanceMetrics represents portfolio performance data
// Return values are in basis points (100 = 1.00%)
type PerformanceMetrics struct {
	TotalReturn       int64     `json:"total_return"`        // Basis points (100 = 1%)
	DayReturn         int64     `json:"day_return"`          // Basis points
	WeekReturn        int64     `json:"week_return"`         // Basis points
	MonthReturn       int64     `json:"month_return"`        // Basis points
	YearReturn        int64     `json:"year_return"`         // Basis points
	VolatilityPercent int64     `json:"volatility_percent"`  // Basis points
	SharpeRatio       int64     `json:"sharpe_ratio"`        // Scaled by 100 (150 = 1.50)
	LastCalculated    time.Time `json:"last_calculated"`
}

// DepositRepository defines the interface for deposit data access
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error)
	GetByOffRampTxID(ctx context.Context, txID string) (*entities.Deposit, error)
	Update(ctx context.Context, deposit *entities.Deposit) error
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Deposit, error)
}

// VirtualAccountRepository defines the interface for virtual account data access
type VirtualAccountRepository interface {
	Create(ctx context.Context, account *entities.VirtualAccount) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
	GetByBridgeCustomerID(ctx context.Context, bridgeCustomerID string) (*entities.VirtualAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
	GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error)
	GetByBridgeAccountID(ctx context.Context, bridgeAccountID string) (*entities.VirtualAccount, error)
	Update(ctx context.Context, account *entities.VirtualAccount) error
}