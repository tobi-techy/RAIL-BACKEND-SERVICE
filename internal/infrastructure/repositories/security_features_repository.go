package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

type SecurityFeaturesRepository struct {
	db *sqlx.DB
}

func NewSecurityFeaturesRepository(db *sqlx.DB) *SecurityFeaturesRepository {
	return &SecurityFeaturesRepository{db: db}
}

// === Risk Assessments ===

func (r *SecurityFeaturesRepository) CreateRiskAssessment(ctx context.Context, a *entities.TransactionRiskAssessment) error {
	signals, _ := json.Marshal(a.Signals)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO transaction_risk_assessments (id, user_id, transaction_type, amount, risk_score, risk_level, action, signals, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.UserID, a.TransactionType, a.Amount, a.RiskScore, a.RiskLevel, a.Action, signals, a.CreatedAt)
	return err
}

func (r *SecurityFeaturesRepository) GetUserAvgAmount(ctx context.Context, userID uuid.UUID, txType string) (decimal.Decimal, error) {
	var avg sql.NullFloat64
	err := r.db.QueryRowContext(ctx,
		`SELECT AVG(amount) FROM transaction_risk_assessments WHERE user_id = $1 AND transaction_type = $2 AND created_at > $3`,
		userID, txType, time.Now().Add(-90*24*time.Hour)).Scan(&avg)
	if err != nil || !avg.Valid {
		return decimal.Zero, err
	}
	return decimal.NewFromFloat(avg.Float64), nil
}

func (r *SecurityFeaturesRepository) CountRecentTransactions(ctx context.Context, userID uuid.UUID, window time.Duration) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transaction_risk_assessments WHERE user_id = $1 AND created_at > $2`,
		userID, time.Now().Add(-window)).Scan(&count)
	return count, err
}

// === Whitelisted Addresses ===

func (r *SecurityFeaturesRepository) CreateWhitelistedAddress(ctx context.Context, a *entities.WhitelistedAddress) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO whitelisted_addresses (id, user_id, chain, address, label, status, cooling_until, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.UserID, a.Chain, a.Address, a.Label, a.Status, a.CoolingUntil, a.CreatedAt, a.UpdatedAt)
	return err
}

func (r *SecurityFeaturesRepository) GetWhitelistedAddresses(ctx context.Context, userID uuid.UUID) ([]entities.WhitelistedAddress, error) {
	var addrs []entities.WhitelistedAddress
	err := r.db.SelectContext(ctx, &addrs,
		`SELECT id, user_id, chain, address, label, status, cooling_until, created_at, updated_at
		 FROM whitelisted_addresses WHERE user_id = $1 AND status != 'removed' ORDER BY created_at DESC`, userID)
	return addrs, err
}

func (r *SecurityFeaturesRepository) GetWhitelistedAddress(ctx context.Context, id, userID uuid.UUID) (*entities.WhitelistedAddress, error) {
	var a entities.WhitelistedAddress
	err := r.db.GetContext(ctx, &a,
		`SELECT id, user_id, chain, address, label, status, cooling_until, created_at, updated_at
		 FROM whitelisted_addresses WHERE id = $1 AND user_id = $2`, id, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

func (r *SecurityFeaturesRepository) RemoveWhitelistedAddress(ctx context.Context, id, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE whitelisted_addresses SET status = 'removed', updated_at = NOW() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func (r *SecurityFeaturesRepository) FindWhitelistedAddress(ctx context.Context, userID uuid.UUID, chain, address string) (*entities.WhitelistedAddress, error) {
	var a entities.WhitelistedAddress
	err := r.db.GetContext(ctx, &a,
		`SELECT id, user_id, chain, address, label, status, cooling_until, created_at, updated_at
		 FROM whitelisted_addresses WHERE user_id = $1 AND chain = $2 AND address = $3 AND status != 'removed'`, userID, chain, address)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

// === Session Anomalies ===

func (r *SecurityFeaturesRepository) CreateSessionAnomaly(ctx context.Context, a *entities.SessionAnomaly) error {
	details, _ := json.Marshal(a.Details)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO session_anomalies (id, user_id, anomaly_type, severity, details, ip_address, country, city, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.UserID, a.AnomalyType, a.Severity, details, a.IPAddress, a.Country, a.City, a.CreatedAt)
	return err
}

func (r *SecurityFeaturesRepository) GetLastSession(ctx context.Context, userID uuid.UUID) (ip, country, city, userAgent string, loginAt time.Time, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT ip_address, COALESCE(country,''), COALESCE(city,''), COALESCE(details->>'user_agent',''), created_at
		 FROM session_anomalies WHERE user_id = $1 AND anomaly_type = 'session_baseline' ORDER BY created_at DESC LIMIT 1`, userID).
		Scan(&ip, &country, &city, &userAgent, &loginAt)
	if err == sql.ErrNoRows {
		err = nil
	}
	return
}

// === Withdrawal Limit Usage ===

func (r *SecurityFeaturesRepository) RecordWithdrawalUsage(ctx context.Context, u *entities.WithdrawalLimitUsage) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO withdrawal_limit_usage (id, user_id, amount, period_type, period_key, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.UserID, u.Amount, u.PeriodType, u.PeriodKey, u.CreatedAt)
	return err
}

func (r *SecurityFeaturesRepository) GetPeriodUsage(ctx context.Context, userID uuid.UUID, periodType, periodKey string) (decimal.Decimal, error) {
	var total sql.NullFloat64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM withdrawal_limit_usage WHERE user_id = $1 AND period_type = $2 AND period_key = $3`,
		userID, periodType, periodKey).Scan(&total)
	if err != nil || !total.Valid {
		return decimal.Zero, err
	}
	return decimal.NewFromFloat(total.Float64), nil
}

// === MFA Challenges ===

func (r *SecurityFeaturesRepository) CreateMFAChallenge(ctx context.Context, c *entities.MFAChallenge) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO mfa_challenges (id, user_id, challenge_type, code_hash, expires_at, verified, attempts, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.UserID, c.ChallengeType, c.CodeHash, c.ExpiresAt, c.Verified, c.Attempts, c.CreatedAt)
	return err
}

func (r *SecurityFeaturesRepository) GetActiveMFAChallenge(ctx context.Context, userID uuid.UUID, challengeType entities.MFAChallengeType) (*entities.MFAChallenge, error) {
	var c entities.MFAChallenge
	err := r.db.GetContext(ctx, &c,
		`SELECT id, user_id, challenge_type, code_hash, expires_at, verified, attempts, created_at
		 FROM mfa_challenges WHERE user_id = $1 AND challenge_type = $2 AND expires_at > NOW() AND verified = false
		 ORDER BY created_at DESC LIMIT 1`, userID, challengeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &c, err
}

func (r *SecurityFeaturesRepository) VerifyMFAChallenge(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE mfa_challenges SET verified = true WHERE id = $1`, id)
	return err
}

func (r *SecurityFeaturesRepository) IncrementMFAAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE mfa_challenges SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}
