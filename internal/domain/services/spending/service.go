package spending

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Repository defines the queries the spending service needs.
type Repository interface {
	GetSpendingByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error)
	GetSpendingByMerchant(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingByMerchant, error)
	GetSpendingByDay(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByPeriod, error)
	GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error)
	GetRecentOutflows(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error)
}

// Summary is the full spending analysis for a period.
type Summary struct {
	Total      decimal.Decimal              `json:"total"`
	TxCount    int                          `json:"transaction_count"`
	Categories []entities.SpendingByCategory `json:"categories"`
	Merchants  []entities.SpendingByMerchant `json:"top_merchants"`
	DailyTrend []entities.SpendingByPeriod   `json:"daily_trend"`
	PeriodDays int                          `json:"period_days"`
	DailyAvg   decimal.Decimal              `json:"daily_average"`
}

// Service provides spending analysis.
type Service struct {
	repo Repository
}

// NewService creates a new spending analysis service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetSummary returns a full spending summary for the given period.
func (s *Service) GetSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (*Summary, error) {
	total, count, err := s.repo.GetSpendingTotal(ctx, userID, start, end)
	if err != nil {
		return nil, err
	}

	cats, err := s.repo.GetSpendingByCategory(ctx, userID, start, end)
	if err != nil {
		return nil, err
	}

	merchants, err := s.repo.GetSpendingByMerchant(ctx, userID, start, end, 10)
	if err != nil {
		return nil, err
	}

	daily, err := s.repo.GetSpendingByDay(ctx, userID, start, end)
	if err != nil {
		return nil, err
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	avg := total.Div(decimal.NewFromInt(int64(days)))

	return &Summary{
		Total:      total,
		TxCount:    count,
		Categories: cats,
		Merchants:  merchants,
		DailyTrend: daily,
		PeriodDays: days,
		DailyAvg:   avg,
	}, nil
}

// GetTransactions returns individual outflow transactions for the given period.
func (s *Service) GetTransactions(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error) {
	return s.repo.GetRecentOutflows(ctx, userID, start, end, limit)
}

// MonthStart returns the first day of the current month.
func MonthStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// MonthEnd returns the start of next month.
func MonthEnd() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}
