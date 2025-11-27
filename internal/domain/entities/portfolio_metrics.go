package entities

import (
	"time"

	"github.com/google/uuid"
)

// PerformancePoint represents a time-series point of portfolio performance
// (e.g. NAV and PnL for a given date).
type PerformancePoint struct {
	Date  time.Time `json:"date"`
	Value float64   `json:"value"`
	PnL   float64   `json:"pnl"`
}

// PositionMetrics represents aggregated metrics for a single basket position
// in a user's portfolio.
type PositionMetrics struct {
	BasketID        uuid.UUID `json:"basket_id"`
	BasketName      string    `json:"basket_name"`
	Quantity        float64   `json:"quantity"`
	AvgPrice        float64   `json:"avg_price"`
	CurrentValue    float64   `json:"current_value"`
	UnrealizedPL    float64   `json:"unrealized_pl"`
	UnrealizedPLPct float64   `json:"unrealized_pl_pct"`
	Weight          float64   `json:"weight"`
}

// PortfolioMetrics captures high-level metrics for a user's portfolio,
// including total value, per-position metrics, and allocation breakdown.
type PortfolioMetrics struct {
	TotalValue         float64                     `json:"total_value"`
	Positions          []PositionMetrics           `json:"positions"`
	AllocationByBasket map[string]float64         `json:"allocation_by_basket"`
	PerformanceHistory []PerformancePoint          `json:"performance_history,omitempty"`
	RiskMetrics        map[string]float64         `json:"risk_metrics,omitempty"`
}
