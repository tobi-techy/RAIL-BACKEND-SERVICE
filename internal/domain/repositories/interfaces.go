package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

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
type Portfolio struct {
	UserID        uuid.UUID           `json:"user_id"`
	TotalValue    float64             `json:"total_value"`
	CashBalance   float64             `json:"cash_balance"`
	Holdings      []*Holding          `json:"holdings"`
	Performance   *PerformanceMetrics `json:"performance"`
	LastUpdated   time.Time           `json:"last_updated"`
}

// Holding represents a user's position in an asset
type Holding struct {
	Symbol      string    `json:"symbol"`
	Quantity    float64   `json:"quantity"`
	MarketValue float64   `json:"market_value"`
	CostBasis   float64   `json:"cost_basis"`
	LastPrice   float64   `json:"last_price"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Transaction represents a portfolio transaction
type Transaction struct {
	ID          uuid.UUID   `json:"id"`
	UserID      uuid.UUID   `json:"user_id"`
	Type        string      `json:"type"` // buy, sell, dividend, etc.
	Symbol      string      `json:"symbol"`
	Quantity    float64     `json:"quantity"`
	Price       float64     `json:"price"`
	Amount      float64     `json:"amount"`
	Timestamp   time.Time   `json:"timestamp"`
}

// PerformanceMetrics represents portfolio performance data
type PerformanceMetrics struct {
	TotalReturn       float64   `json:"total_return"`
	DayReturn         float64   `json:"day_return"`
	WeekReturn        float64   `json:"week_return"`
	MonthReturn       float64   `json:"month_return"`
	YearReturn        float64   `json:"year_return"`
	VolatilityPercent float64   `json:"volatility_percent"`
	SharpeRatio       float64   `json:"sharpe_ratio"`
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
	GetByDueAccountID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
	Update(ctx context.Context, account *entities.VirtualAccount) error
}