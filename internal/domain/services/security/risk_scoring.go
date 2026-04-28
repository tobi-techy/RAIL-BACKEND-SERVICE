package security

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

type RiskScoringService struct {
	repo   *repositories.SecurityFeaturesRepository
	logger *zap.Logger
}

func NewRiskScoringService(repo *repositories.SecurityFeaturesRepository, logger *zap.Logger) *RiskScoringService {
	return &RiskScoringService{repo: repo, logger: logger}
}

type RiskInput struct {
	UserID      uuid.UUID
	Amount      decimal.Decimal
	TxType      string
	DeviceTrust entities.DeviceTrustLevel
	AccountAge  time.Duration
	Hour        int // 0-23
}

func (s *RiskScoringService) ScoreTransaction(ctx context.Context, input RiskInput) (*entities.TransactionRiskAssessment, error) {
	var score float64
	signals := map[string]interface{}{}

	// Amount anomaly
	avg, _ := s.repo.GetUserAvgAmount(ctx, input.UserID, input.TxType)
	if !avg.IsZero() {
		ratio, _ := input.Amount.Div(avg).Float64()
		if ratio > 3.0 {
			s.addSignal(&score, signals, "amount_anomaly", math.Min((ratio-1)*0.1, 0.3))
		}
	}

	// Velocity
	hourCount, _ := s.repo.CountRecentTransactions(ctx, input.UserID, time.Hour)
	dayCount, _ := s.repo.CountRecentTransactions(ctx, input.UserID, 24*time.Hour)
	if hourCount > 5 {
		s.addSignal(&score, signals, "high_hourly_velocity", 0.2)
	}
	if dayCount > 20 {
		s.addSignal(&score, signals, "high_daily_velocity", 0.15)
	}

	// Time of day (2am-5am local)
	if input.Hour >= 2 && input.Hour <= 5 {
		s.addSignal(&score, signals, "unusual_hour", 0.1)
	}

	// Device trust
	switch input.DeviceTrust {
	case entities.DeviceTrustNew:
		s.addSignal(&score, signals, "new_device", 0.15)
	case entities.DeviceTrustSuspicious:
		s.addSignal(&score, signals, "suspicious_device", 0.25)
	}

	// Account age
	if input.AccountAge < 7*24*time.Hour {
		s.addSignal(&score, signals, "new_account", 0.15)
	}

	score = math.Min(score, 1.0)

	assessment := &entities.TransactionRiskAssessment{
		ID:              uuid.New(),
		UserID:          input.UserID,
		TransactionType: input.TxType,
		Amount:          input.Amount,
		RiskScore:       score,
		RiskLevel:       riskLevel(score),
		Action:          riskAction(score),
		Signals:         signals,
		CreatedAt:       time.Now(),
	}

	if err := s.repo.CreateRiskAssessment(ctx, assessment); err != nil {
		s.logger.Error("Failed to store risk assessment", zap.Error(err))
	}

	return assessment, nil
}

func (s *RiskScoringService) addSignal(score *float64, signals map[string]interface{}, name string, weight float64) {
	*score += weight
	signals[name] = weight
}

func riskLevel(score float64) entities.TxRiskLevel {
	switch {
	case score >= 0.8:
		return entities.TxRiskLevelCritical
	case score >= 0.6:
		return entities.TxRiskLevelHigh
	case score >= 0.3:
		return entities.TxRiskLevelMedium
	default:
		return entities.TxRiskLevelLow
	}
}

func riskAction(score float64) entities.TxRiskAction {
	switch {
	case score >= 0.8:
		return entities.TxRiskActionBlock
	case score >= 0.6:
		return entities.TxRiskActionFlagReview
	case score >= 0.3:
		return entities.TxRiskActionStepUpAuth
	default:
		return entities.TxRiskActionAllow
	}
}
