package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingFraudRepository implements repositories.OnboardingFraudRepository.
type OnboardingFraudRepository struct {
	db *sqlx.DB
}

func NewOnboardingFraudRepository(db *sqlx.DB) *OnboardingFraudRepository {
	return &OnboardingFraudRepository{db: db}
}

func (r *OnboardingFraudRepository) RecordDeviceAccountLink(ctx context.Context, link *entities.DeviceAccountLink) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO device_account_links (id, device_fingerprint, user_id, ip_address, user_agent, event_type, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		link.ID, link.DeviceFingerprint, link.UserID, link.IPAddress, link.UserAgent, link.EventType, link.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert device_account_link: %w", err)
	}
	return nil
}

// CountCorrelationSignals returns all three cross-account correlation counts in one query.
func (r *OnboardingFraudRepository) CountCorrelationSignals(ctx context.Context, fingerprint, ip string, deviceSince, ipVelocitySince, ipClusterSince time.Time) (int, int, int, error) {
	var accountsByFP, onboardingsFromIP, accountsByIP int

	err := r.db.QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT COUNT(DISTINCT user_id) FROM device_account_links WHERE device_fingerprint = $1 AND created_at >= $2), 0),
			COALESCE((SELECT COUNT(*) FROM device_account_links WHERE ip_address = $3 AND event_type = 'onboarding' AND created_at >= $4), 0),
			COALESCE((SELECT COUNT(DISTINCT user_id) FROM device_account_links WHERE ip_address = $3 AND created_at >= $5), 0)`,
		fingerprint, deviceSince, ip, ipVelocitySince, ipClusterSince,
	).Scan(&accountsByFP, &onboardingsFromIP, &accountsByIP)

	if err != nil {
		return 0, 0, 0, fmt.Errorf("count correlation signals: %w", err)
	}
	return accountsByFP, onboardingsFromIP, accountsByIP, nil
}

func (r *OnboardingFraudRepository) SaveRiskAssessment(ctx context.Context, a *entities.OnboardingRiskAssessment) error {
	signalsJSON, err := json.Marshal(a.Signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO onboarding_risk_assessments (id, user_id, event_type, risk_score, risk_level, action, signals, ip_address, device_fingerprint, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.ID, a.UserID, a.EventType, a.RiskScore, a.RiskLevel, a.Action, signalsJSON, a.IPAddress, a.DeviceFingerprint, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert risk assessment: %w", err)
	}
	return nil
}

func (r *OnboardingFraudRepository) GetLatestAssessment(ctx context.Context, userID uuid.UUID) (*entities.OnboardingRiskAssessment, error) {
	var a entities.OnboardingRiskAssessment
	var signalsJSON []byte

	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, event_type, risk_score, risk_level, action, signals, ip_address, device_fingerprint, created_at
		 FROM onboarding_risk_assessments WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		userID).Scan(&a.ID, &a.UserID, &a.EventType, &a.RiskScore, &a.RiskLevel, &a.Action, &signalsJSON, &a.IPAddress, &a.DeviceFingerprint, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get latest assessment: %w", err)
	}

	if signalsJSON != nil {
		json.Unmarshal(signalsJSON, &a.Signals)
	}
	return &a, nil
}
