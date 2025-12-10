package services

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type PortfolioService struct {
	logger *zap.Logger
}

func NewPortfolioService(logger *zap.Logger) *PortfolioService {
	return &PortfolioService{logger: logger}
}

func (s *PortfolioService) CalculateRebalance(ctx context.Context, portfolioID uuid.UUID, target, current map[string]decimal.Decimal) ([]entities.RebalanceTrade, error) {
	trades := []entities.RebalanceTrade{}
	
	for symbol, targetPct := range target {
		currentPct := current[symbol]
		diff := targetPct.Sub(currentPct)
		
		if diff.Abs().GreaterThan(decimal.NewFromFloat(0.05)) {
			action := "buy"
			if diff.IsNegative() {
				action = "sell"
			}
			
			trades = append(trades, entities.RebalanceTrade{
				Symbol:   symbol,
				Action:   action,
				Quantity: diff.Abs(),
				Price:    decimal.Zero,
			})
		}
	}
	
	s.logger.Info("Rebalance calculated", 
		zap.String("portfolio_id", portfolioID.String()),
		zap.Int("trades", len(trades)))
	
	return trades, nil
}

func (s *PortfolioService) GenerateTaxReport(ctx context.Context, userID uuid.UUID, year int) (*entities.TaxReport, error) {
	report := &entities.TaxReport{
		ID:             uuid.New(),
		UserID:         userID,
		TaxYear:        year,
		TotalGains:     decimal.Zero,
		TotalLosses:    decimal.Zero,
		ShortTermGains: decimal.Zero,
		LongTermGains:  decimal.Zero,
		ReportURL:      "tax_report_url",
		GeneratedAt:    time.Now(),
	}
	
	s.logger.Info("Tax report generated", 
		zap.String("user_id", userID.String()),
		zap.Int("year", year))
	
	return report, nil
}
