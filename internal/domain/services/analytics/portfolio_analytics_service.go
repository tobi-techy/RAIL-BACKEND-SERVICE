package analytics

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// SnapshotRepository interface for portfolio snapshots
type SnapshotRepository interface {
	Create(ctx context.Context, snapshot *entities.PortfolioSnapshot) error
	GetByUserIDAndDateRange(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]*entities.PortfolioSnapshot, error)
	GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.PortfolioSnapshot, error)
	GetByDate(ctx context.Context, userID uuid.UUID, date time.Time) (*entities.PortfolioSnapshot, error)
}

// PositionProvider interface for getting current positions
type PositionProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error)
}

// AccountProvider interface for getting account data
type AccountProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
}

// PortfolioAnalyticsService handles performance and risk calculations
type PortfolioAnalyticsService struct {
	snapshotRepo    SnapshotRepository
	positionRepo    PositionProvider
	accountRepo     AccountProvider
	logger          *zap.Logger
}

func NewPortfolioAnalyticsService(
	snapshotRepo SnapshotRepository,
	positionRepo PositionProvider,
	accountRepo AccountProvider,
	logger *zap.Logger,
) *PortfolioAnalyticsService {
	return &PortfolioAnalyticsService{
		snapshotRepo: snapshotRepo,
		positionRepo: positionRepo,
		accountRepo:  accountRepo,
		logger:       logger,
	}
}

// TakeSnapshot captures current portfolio state
func (s *PortfolioAnalyticsService) TakeSnapshot(ctx context.Context, userID uuid.UUID) error {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil || account == nil {
		return err
	}

	positions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	var investedValue, costBasis, dayGainLoss decimal.Decimal
	for _, pos := range positions {
		investedValue = investedValue.Add(pos.MarketValue)
		costBasis = costBasis.Add(pos.CostBasis)
		dayGainLoss = dayGainLoss.Add(pos.MarketValue.Sub(pos.Qty.Mul(pos.LastdayPrice)))
	}

	totalValue := account.Cash.Add(investedValue)
	totalGainLoss := investedValue.Sub(costBasis)
	var totalGainLossPct, dayGainLossPct decimal.Decimal
	if costBasis.GreaterThan(decimal.Zero) {
		totalGainLossPct = totalGainLoss.Div(costBasis).Mul(decimal.NewFromInt(100))
	}
	if investedValue.Sub(dayGainLoss).GreaterThan(decimal.Zero) {
		dayGainLossPct = dayGainLoss.Div(investedValue.Sub(dayGainLoss)).Mul(decimal.NewFromInt(100))
	}

	snapshot := &entities.PortfolioSnapshot{
		ID:               uuid.New(),
		UserID:           userID,
		TotalValue:       totalValue,
		CashValue:        account.Cash,
		InvestedValue:    investedValue,
		TotalCostBasis:   costBasis,
		TotalGainLoss:    totalGainLoss,
		TotalGainLossPct: totalGainLossPct,
		DayGainLoss:      dayGainLoss,
		DayGainLossPct:   dayGainLossPct,
		SnapshotDate:     time.Now().Truncate(24 * time.Hour),
		CreatedAt:        time.Now(),
	}

	return s.snapshotRepo.Create(ctx, snapshot)
}

// GetPerformanceMetrics calculates portfolio performance
func (s *PortfolioAnalyticsService) GetPerformanceMetrics(ctx context.Context, userID uuid.UUID) (*entities.PerformanceMetrics, error) {
	now := time.Now()
	yearAgo := now.AddDate(-1, 0, 0)

	snapshots, err := s.snapshotRepo.GetByUserIDAndDateRange(ctx, userID, yearAgo, now)
	if err != nil {
		return nil, err
	}
	if len(snapshots) < 2 {
		return &entities.PerformanceMetrics{}, nil
	}

	latest := snapshots[len(snapshots)-1]
	first := snapshots[0]

	metrics := &entities.PerformanceMetrics{
		TotalReturn:    latest.TotalGainLoss,
		TotalReturnPct: latest.TotalGainLossPct,
		DayReturn:      latest.DayGainLoss,
		DayReturnPct:   latest.DayGainLossPct,
	}

	// Calculate period returns
	metrics.WeekReturn, metrics.WeekReturnPct = s.calcPeriodReturn(snapshots, 7)
	metrics.MonthReturn, metrics.MonthReturnPct = s.calcPeriodReturn(snapshots, 30)
	metrics.YearReturn, metrics.YearReturnPct = s.calcPeriodReturn(snapshots, 365)

	// Calculate CAGR
	if first.TotalValue.GreaterThan(decimal.Zero) {
		days := now.Sub(first.SnapshotDate).Hours() / 24
		if days > 0 {
			totalReturn := latest.TotalValue.Div(first.TotalValue).InexactFloat64()
			cagr := math.Pow(totalReturn, 365/days) - 1
			metrics.CAGR = decimal.NewFromFloat(cagr * 100)
		}
	}

	// Calculate daily returns for Sharpe ratio and best/worst days
	var dailyReturns []float64
	var bestDay, worstDay decimal.Decimal
	winDays, loseDays := 0, 0

	for i := 1; i < len(snapshots); i++ {
		prev := snapshots[i-1]
		curr := snapshots[i]
		if prev.TotalValue.GreaterThan(decimal.Zero) {
			ret := curr.TotalValue.Sub(prev.TotalValue).Div(prev.TotalValue)
			dailyReturns = append(dailyReturns, ret.InexactFloat64())
			if ret.GreaterThan(bestDay) {
				bestDay = ret
			}
			if ret.LessThan(worstDay) {
				worstDay = ret
			}
			if ret.GreaterThan(decimal.Zero) {
				winDays++
			} else if ret.LessThan(decimal.Zero) {
				loseDays++
			}
		}
	}

	metrics.BestDay = bestDay.Mul(decimal.NewFromInt(100))
	metrics.WorstDay = worstDay.Mul(decimal.NewFromInt(100))
	metrics.WinningDays = winDays
	metrics.LosingDays = loseDays

	// Sharpe ratio (assuming 2% risk-free rate)
	if len(dailyReturns) > 1 {
		avgReturn := mean(dailyReturns)
		stdDev := stddev(dailyReturns)
		if stdDev > 0 {
			riskFreeDaily := 0.02 / 252
			sharpe := (avgReturn - riskFreeDaily) / stdDev * math.Sqrt(252)
			metrics.SharpeRatio = decimal.NewFromFloat(sharpe)
		}
	}

	return metrics, nil
}

// GetRiskMetrics calculates portfolio risk assessment
func (s *PortfolioAnalyticsService) GetRiskMetrics(ctx context.Context, userID uuid.UUID) (*entities.RiskMetrics, error) {
	now := time.Now()
	yearAgo := now.AddDate(-1, 0, 0)

	snapshots, err := s.snapshotRepo.GetByUserIDAndDateRange(ctx, userID, yearAgo, now)
	if err != nil {
		return nil, err
	}

	metrics := &entities.RiskMetrics{RiskLevel: "unknown"}
	if len(snapshots) < 10 {
		return metrics, nil
	}

	// Calculate daily returns
	var dailyReturns []float64
	for i := 1; i < len(snapshots); i++ {
		prev := snapshots[i-1]
		curr := snapshots[i]
		if prev.TotalValue.GreaterThan(decimal.Zero) {
			ret := curr.TotalValue.Sub(prev.TotalValue).Div(prev.TotalValue).InexactFloat64()
			dailyReturns = append(dailyReturns, ret)
		}
	}

	// Annualized volatility
	if len(dailyReturns) > 1 {
		vol := stddev(dailyReturns) * math.Sqrt(252)
		metrics.Volatility = decimal.NewFromFloat(vol * 100)
	}

	// Max drawdown
	maxDrawdown, maxDrawdownDate := s.calcMaxDrawdown(snapshots)
	metrics.MaxDrawdown = maxDrawdown
	metrics.MaxDrawdownDate = maxDrawdownDate

	// 95% VaR (parametric)
	if len(dailyReturns) > 1 {
		sort.Float64s(dailyReturns)
		idx := int(float64(len(dailyReturns)) * 0.05)
		if idx < len(dailyReturns) {
			metrics.VaR95 = decimal.NewFromFloat(dailyReturns[idx] * -100)
		}
	}

	// Risk level classification
	vol := metrics.Volatility.InexactFloat64()
	switch {
	case vol < 10:
		metrics.RiskLevel = "low"
	case vol < 20:
		metrics.RiskLevel = "moderate"
	default:
		metrics.RiskLevel = "high"
	}

	return metrics, nil
}

// GetDiversificationAnalysis analyzes portfolio diversification
func (s *PortfolioAnalyticsService) GetDiversificationAnalysis(ctx context.Context, userID uuid.UUID) (*entities.DiversificationAnalysis, error) {
	positions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	analysis := &entities.DiversificationAnalysis{
		SectorAllocation:    make(map[string]decimal.Decimal),
		AssetTypeAllocation: make(map[string]decimal.Decimal),
		TopHoldings:         []entities.HoldingWeight{},
		Recommendations:     []string{},
	}

	if len(positions) == 0 {
		analysis.DiversificationScore = 0
		return analysis, nil
	}

	// Calculate total value
	var totalValue decimal.Decimal
	for _, pos := range positions {
		totalValue = totalValue.Add(pos.MarketValue)
	}

	if totalValue.IsZero() {
		return analysis, nil
	}

	// Build holdings with weights
	holdings := make([]entities.HoldingWeight, len(positions))
	var hhi float64 // Herfindahl-Hirschman Index

	for i, pos := range positions {
		weight := pos.MarketValue.Div(totalValue).Mul(decimal.NewFromInt(100))
		holdings[i] = entities.HoldingWeight{
			Symbol:      pos.Symbol,
			Weight:      weight,
			Value:       pos.MarketValue,
			GainLoss:    pos.UnrealizedPL,
			GainLossPct: pos.UnrealizedPLPC,
		}
		hhi += math.Pow(weight.InexactFloat64(), 2)

		// Classify asset type (simplified)
		assetType := "stock"
		if len(pos.Symbol) > 3 && (pos.Symbol[len(pos.Symbol)-1] == 'X' || pos.Symbol == "SPY" || pos.Symbol == "QQQ" || pos.Symbol == "VTI") {
			assetType = "etf"
		}
		analysis.AssetTypeAllocation[assetType] = analysis.AssetTypeAllocation[assetType].Add(weight)
	}

	// Sort by weight descending
	sort.Slice(holdings, func(i, j int) bool {
		return holdings[i].Weight.GreaterThan(holdings[j].Weight)
	})

	// Top 5 holdings
	if len(holdings) > 5 {
		analysis.TopHoldings = holdings[:5]
	} else {
		analysis.TopHoldings = holdings
	}

	// Concentration risk (HHI)
	analysis.ConcentrationRisk = decimal.NewFromFloat(hhi)

	// Diversification score (inverse of HHI, normalized)
	// Perfect diversification across N assets = 10000/N
	// Score 100 = well diversified, 0 = concentrated
	maxHHI := 10000.0 // single stock
	minHHI := 10000.0 / float64(max(len(positions), 1))
	if maxHHI > minHHI {
		score := (maxHHI - hhi) / (maxHHI - minHHI) * 100
		analysis.DiversificationScore = int(math.Min(100, math.Max(0, score)))
	}

	// Recommendations
	if len(positions) < 5 {
		analysis.Recommendations = append(analysis.Recommendations, "Consider adding more positions to improve diversification")
	}
	if len(analysis.TopHoldings) > 0 && analysis.TopHoldings[0].Weight.GreaterThan(decimal.NewFromInt(30)) {
		analysis.Recommendations = append(analysis.Recommendations, "Top holding exceeds 30% - consider rebalancing")
	}
	if hhi > 2500 {
		analysis.Recommendations = append(analysis.Recommendations, "Portfolio is highly concentrated - consider spreading across more assets")
	}

	return analysis, nil
}

func (s *PortfolioAnalyticsService) calcPeriodReturn(snapshots []*entities.PortfolioSnapshot, days int) (decimal.Decimal, decimal.Decimal) {
	if len(snapshots) < 2 {
		return decimal.Zero, decimal.Zero
	}
	latest := snapshots[len(snapshots)-1]
	targetDate := latest.SnapshotDate.AddDate(0, 0, -days)

	var closest *entities.PortfolioSnapshot
	for _, snap := range snapshots {
		if snap.SnapshotDate.Before(targetDate) || snap.SnapshotDate.Equal(targetDate) {
			closest = snap
		}
	}
	if closest == nil || closest.TotalValue.IsZero() {
		return decimal.Zero, decimal.Zero
	}

	ret := latest.TotalValue.Sub(closest.TotalValue)
	retPct := ret.Div(closest.TotalValue).Mul(decimal.NewFromInt(100))
	return ret, retPct
}

func (s *PortfolioAnalyticsService) calcMaxDrawdown(snapshots []*entities.PortfolioSnapshot) (decimal.Decimal, *time.Time) {
	if len(snapshots) == 0 {
		return decimal.Zero, nil
	}

	peak := snapshots[0].TotalValue
	maxDD := decimal.Zero
	var maxDDDate *time.Time

	for _, snap := range snapshots {
		if snap.TotalValue.GreaterThan(peak) {
			peak = snap.TotalValue
		}
		if peak.GreaterThan(decimal.Zero) {
			dd := peak.Sub(snap.TotalValue).Div(peak).Mul(decimal.NewFromInt(100))
			if dd.GreaterThan(maxDD) {
				maxDD = dd
				t := snap.SnapshotDate
				maxDDDate = &t
			}
		}
	}
	return maxDD, maxDDDate
}

func mean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func stddev(data []float64) float64 {
	if len(data) < 2 {
		return 0
	}
	m := mean(data)
	sum := 0.0
	for _, v := range data {
		sum += (v - m) * (v - m)
	}
	return math.Sqrt(sum / float64(len(data)-1))
}
