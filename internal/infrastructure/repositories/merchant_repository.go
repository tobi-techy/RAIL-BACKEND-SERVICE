package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/shopspring/decimal"
)

// MerchantRepository provides merchant spending intelligence.
type MerchantRepository struct {
	db *sqlx.DB
}

func NewMerchantRepository(db *sqlx.DB) *MerchantRepository {
	return &MerchantRepository{db: db}
}

type merchantRow struct {
	Merchant   string          `db:"merchant"`
	VisitCount int             `db:"visit_count"`
	TotalSpent decimal.Decimal `db:"total_spent"`
	AvgSpend   decimal.Decimal `db:"avg_spend"`
	LastVisit  time.Time       `db:"last_visit"`
	FirstVisit time.Time       `db:"first_visit"`
	Category   string          `db:"category"`
	ThisMonth  decimal.Decimal `db:"this_month"`
	LastMonth  decimal.Decimal `db:"last_month"`
}

const merchantUnionQuery = `
	SELECT merchant, category, amount, created_at FROM (
		SELECT COALESCE(merchant_name, 'Unknown') AS merchant,
			COALESCE(merchant_category, 'Uncategorized') AS category,
			amount, created_at
		FROM card_transactions WHERE user_id = $1 AND status = 'completed'
		UNION ALL
		SELECT COALESCE(merchant, 'Unknown') AS merchant,
			COALESCE(category, 'Uncategorized') AS category,
			amount, created_at
		FROM receipt_scans WHERE user_id = $1
	) combined`

func (r *MerchantRepository) GetTopMerchants(ctx context.Context, userID uuid.UUID, limit int) ([]ai.MerchantProfile, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	now := time.Now().UTC()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := thisMonthStart.AddDate(0, -1, 0)

	query := fmt.Sprintf(`
		WITH all_txns AS (%s)
		SELECT merchant,
			MAX(category) AS category,
			COUNT(*) AS visit_count,
			SUM(amount) AS total_spent,
			AVG(amount) AS avg_spend,
			MAX(created_at) AS last_visit,
			MIN(created_at) AS first_visit,
			COALESCE(SUM(CASE WHEN created_at >= $2 THEN amount END), 0) AS this_month,
			COALESCE(SUM(CASE WHEN created_at >= $3 AND created_at < $2 THEN amount END), 0) AS last_month
		FROM all_txns
		GROUP BY merchant
		ORDER BY total_spent DESC
		LIMIT $4`, merchantUnionQuery)

	var rows []merchantRow
	if err := r.db.SelectContext(ctx, &rows, query, userID, thisMonthStart, lastMonthStart, limit); err != nil {
		return nil, fmt.Errorf("top merchants: %w", err)
	}

	return toMerchantProfiles(rows), nil
}

func (r *MerchantRepository) GetMerchantProfile(ctx context.Context, userID uuid.UUID, merchant string) (*ai.MerchantProfile, error) {
	now := time.Now().UTC()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := thisMonthStart.AddDate(0, -1, 0)

	query := fmt.Sprintf(`
		WITH all_txns AS (%s)
		SELECT merchant,
			MAX(category) AS category,
			COUNT(*) AS visit_count,
			SUM(amount) AS total_spent,
			AVG(amount) AS avg_spend,
			MAX(created_at) AS last_visit,
			MIN(created_at) AS first_visit,
			COALESCE(SUM(CASE WHEN created_at >= $2 THEN amount END), 0) AS this_month,
			COALESCE(SUM(CASE WHEN created_at >= $3 AND created_at < $2 THEN amount END), 0) AS last_month
		FROM all_txns WHERE LOWER(merchant) = LOWER($4)
		GROUP BY merchant`, merchantUnionQuery)

	var row merchantRow
	if err := r.db.GetContext(ctx, &row, query, userID, thisMonthStart, lastMonthStart, merchant); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("merchant profile: %w", err)
	}

	profiles := toMerchantProfiles([]merchantRow{row})
	return &profiles[0], nil
}

func toMerchantProfiles(rows []merchantRow) []ai.MerchantProfile {
	results := make([]ai.MerchantProfile, len(rows))
	for i, row := range rows {
		months := row.LastVisit.Sub(row.FirstVisit).Hours() / (24 * 30)
		if months < 1 {
			months = 1
		}
		monthlyAvg := row.TotalSpent.Div(decimal.NewFromFloat(months))

		trend := "stable"
		if !row.LastMonth.IsZero() {
			change := row.ThisMonth.Sub(row.LastMonth).Div(row.LastMonth).Mul(decimal.NewFromInt(100))
			if change.GreaterThan(decimal.NewFromInt(10)) {
				trend = "+" + change.StringFixed(0) + "%"
			} else if change.LessThan(decimal.NewFromInt(-10)) {
				trend = change.StringFixed(0) + "%"
			}
		} else if row.ThisMonth.IsPositive() {
			trend = "new this month"
		}

		results[i] = ai.MerchantProfile{
			Merchant:         row.Merchant,
			VisitCount:       row.VisitCount,
			TotalSpent:       row.TotalSpent.StringFixed(2),
			AvgSpend:         row.AvgSpend.StringFixed(2),
			LastVisit:        row.LastVisit.Format("2006-01-02"),
			MonthlyAvg:       monthlyAvg.StringFixed(2),
			TrendVsLastMonth: trend,
			Category:         row.Category,
		}
	}
	return results
}
