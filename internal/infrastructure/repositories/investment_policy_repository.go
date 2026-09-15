package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investment"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Per-user investment limits
// ---------------------------------------------------------------------------

// InvestmentLimitsRepository stores per-user limit overrides.
type InvestmentLimitsRepository struct {
	db *sqlx.DB
}

// NewInvestmentLimitsRepository creates a new investment limits repository.
func NewInvestmentLimitsRepository(db *sqlx.DB) *InvestmentLimitsRepository {
	return &InvestmentLimitsRepository{db: db}
}

type investmentLimitsRow struct {
	ID                uuid.UUID       `db:"id"`
	UserID            uuid.UUID       `db:"user_id"`
	MaxPositionPct    decimal.Decimal `db:"max_position_pct"`
	MaxStrategyPct    decimal.Decimal `db:"max_strategy_pct"`
	MaxTransactionUSD decimal.Decimal `db:"max_transaction_usd"`
	MaxDailyVolumeUSD decimal.Decimal `db:"max_daily_volume_usd"`
	MinCashReserveUSD decimal.Decimal `db:"min_cash_reserve_usd"`
	MinOrderAmountUSD decimal.Decimal `db:"min_order_amount_usd"`
	MaxEnrollments    int             `db:"max_enrollments"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

const investmentLimitsColumns = `id, user_id, max_position_pct, max_strategy_pct, max_transaction_usd,
	max_daily_volume_usd, min_cash_reserve_usd, min_order_amount_usd, max_enrollments, created_at, updated_at`

// Get returns a user's stored limit overrides, or (nil, nil) when they fall
// back to the configured defaults. DailyVolumeUSD is intentionally left zero:
// it is computed from funding history by the service, not stored here.
func (r *InvestmentLimitsRepository) Get(ctx context.Context, userID uuid.UUID) (*entities.InvestmentLimits, error) {
	row := investmentLimitsRow{}
	err := r.db.GetContext(ctx, &row,
		`SELECT `+investmentLimitsColumns+` FROM investment_limits WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment limits: %w", err)
	}
	return &entities.InvestmentLimits{
		MaxPositionPct:    row.MaxPositionPct,
		MaxStrategyPct:    row.MaxStrategyPct,
		MaxTransactionUSD: row.MaxTransactionUSD,
		MaxDailyVolumeUSD: row.MaxDailyVolumeUSD,
		MinCashReserveUSD: row.MinCashReserveUSD,
		MinOrderAmountUSD: row.MinOrderAmountUSD,
		MaxEnrollments:    row.MaxEnrollments,
	}, nil
}

// Upsert stores a user's limit overrides.
func (r *InvestmentLimitsRepository) Upsert(ctx context.Context, userID uuid.UUID, limits *entities.InvestmentLimits) error {
	if limits == nil {
		return fmt.Errorf("investment limits is nil")
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_limits (id, user_id, max_position_pct, max_strategy_pct, max_transaction_usd,
			max_daily_volume_usd, min_cash_reserve_usd, min_order_amount_usd, max_enrollments, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (user_id) DO UPDATE SET
			max_position_pct = EXCLUDED.max_position_pct,
			max_strategy_pct = EXCLUDED.max_strategy_pct,
			max_transaction_usd = EXCLUDED.max_transaction_usd,
			max_daily_volume_usd = EXCLUDED.max_daily_volume_usd,
			min_cash_reserve_usd = EXCLUDED.min_cash_reserve_usd,
			min_order_amount_usd = EXCLUDED.min_order_amount_usd,
			max_enrollments = EXCLUDED.max_enrollments,
			updated_at = EXCLUDED.updated_at`,
		uuid.New(), userID, limits.MaxPositionPct, limits.MaxStrategyPct, limits.MaxTransactionUSD,
		limits.MaxDailyVolumeUSD, limits.MinCashReserveUSD, limits.MinOrderAmountUSD,
		limits.MaxEnrollments, now, now)
	if err != nil {
		return fmt.Errorf("upsert investment limits: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// User profile reader (policy input)
// ---------------------------------------------------------------------------

// InvestmentUserProfileReader supplies the policy layer's view of the user.
type InvestmentUserProfileReader struct {
	db *sqlx.DB
}

// NewInvestmentUserProfileReader creates a user profile reader.
func NewInvestmentUserProfileReader(db *sqlx.DB) *InvestmentUserProfileReader {
	return &InvestmentUserProfileReader{db: db}
}

// GetInvestmentProfile reads the KYC tier/status and country from the existing
// users table.
//
// Column mapping (verified against migrations 001/005/102/184/264): the users
// table stores an integer `kyc_tier` (nullable, floor of 1) and a text
// `kyc_status`, plus a nullable text `country` added by migration 102. The
// investment policy compares the tier as a string ("1"/"2"/"3"), so the
// integer is converted with strconv. A user always resolves here; absence
// returns (nil, nil).
func (r *InvestmentUserProfileReader) GetInvestmentProfile(ctx context.Context, userID uuid.UUID) (*investment.UserProfile, error) {
	var tier int
	var kycStatus string
	var country string
	err := r.db.QueryRowxContext(ctx, `
		SELECT COALESCE(kyc_tier, 1) AS kyc_tier,
		       COALESCE(kyc_status, '') AS kyc_status,
		       COALESCE(country, '') AS country
		FROM users
		WHERE id = $1`, userID).Scan(&tier, &kycStatus, &country)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment user profile: %w", err)
	}
	return &investment.UserProfile{
		UserID:    userID,
		KYCTier:   strconv.Itoa(tier),
		KYCStatus: kycStatus,
		Country:   country,
	}, nil
}
