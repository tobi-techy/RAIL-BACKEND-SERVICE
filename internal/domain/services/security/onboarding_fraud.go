package security

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"go.uber.org/zap"
)

// OnboardingFraudService detects fraud rings that pass KYC with purchased identities.
type OnboardingFraudService struct {
	repo   repositories.OnboardingFraudRepository
	logger *zap.Logger
}

func NewOnboardingFraudService(repo repositories.OnboardingFraudRepository, logger *zap.Logger) *OnboardingFraudService {
	return &OnboardingFraudService{repo: repo, logger: logger}
}

const (
	maxAccountsPerDevice = 2
	maxAccountsPerIP     = 5
	maxOnboardingsPerIP  = 3

	deviceCorrelationWindow  = 30 * 24 * time.Hour
	ipCorrelationWindow      = 30 * 24 * time.Hour
	onboardingVelocityWindow = 24 * time.Hour

	thresholdFlag         = 0.3
	thresholdDelayFunding = 0.5
	thresholdManualReview = 0.7
	thresholdBlock        = 0.9
)

var signalWeights = map[string]float64{
	"device_reuse":        1.5,
	"ip_velocity":         0.8,
	"ip_account_cluster":  0.6,
	"new_account_deposit": 0.4,
	"high_first_deposit":  0.5,
}

// AssessOnboarding evaluates fraud risk when a user completes onboarding.
func (s *OnboardingFraudService) AssessOnboarding(ctx context.Context, userID uuid.UUID, fingerprint, ip, userAgent string) (*entities.OnboardingRiskAssessment, error) {
	if err := s.recordLink(ctx, userID, fingerprint, ip, userAgent, "onboarding"); err != nil {
		s.logger.Error("failed to record device-account link", zap.Error(err))
	}

	signals := s.collectSignals(ctx, fingerprint, ip)
	return s.buildAssessment(ctx, userID, "onboarding_complete", fingerprint, ip, signals)
}

// AssessFirstDeposit evaluates fraud risk on a user's first deposit.
func (s *OnboardingFraudService) AssessFirstDeposit(ctx context.Context, userID uuid.UUID, fingerprint, ip string, depositAmountUSD float64, accountAge time.Duration) (*entities.OnboardingRiskAssessment, error) {
	if err := s.recordLink(ctx, userID, fingerprint, ip, "", "deposit"); err != nil {
		s.logger.Error("failed to record deposit device link", zap.Error(err))
	}

	signals := s.collectSignals(ctx, fingerprint, ip)

	if accountAge < 2*time.Hour {
		signals = append(signals, entities.OnboardingRiskSignal{
			Type: "new_account_deposit", Score: 0.6,
			Weight: signalWeights["new_account_deposit"],
			Detail: "first deposit within 2 hours of onboarding",
		})
	}

	if depositAmountUSD > 500 && accountAge < 24*time.Hour {
		score := depositAmountUSD / 2000.0
		if score > 1.0 {
			score = 1.0
		}
		signals = append(signals, entities.OnboardingRiskSignal{
			Type: "high_first_deposit", Score: score,
			Weight: signalWeights["high_first_deposit"],
			Detail: "large first deposit on new account",
		})
	}

	return s.buildAssessment(ctx, userID, "first_deposit", fingerprint, ip, signals)
}

func (s *OnboardingFraudService) recordLink(ctx context.Context, userID uuid.UUID, fingerprint, ip, userAgent, eventType string) error {
	return s.repo.RecordDeviceAccountLink(ctx, &entities.DeviceAccountLink{
		ID: uuid.New(), DeviceFingerprint: fingerprint, UserID: userID,
		IPAddress: ip, UserAgent: userAgent, EventType: eventType, CreatedAt: time.Now(),
	})
}

func (s *OnboardingFraudService) buildAssessment(ctx context.Context, userID uuid.UUID, eventType, fingerprint, ip string, signals []entities.OnboardingRiskSignal) (*entities.OnboardingRiskAssessment, error) {
	score, level := scoreSignals(signals)
	action := determineAction(score)

	assessment := &entities.OnboardingRiskAssessment{
		ID: uuid.New(), UserID: userID, EventType: eventType,
		RiskScore: score, RiskLevel: level, Action: action,
		Signals: signalsToMap(signals), IPAddress: ip,
		DeviceFingerprint: fingerprint, CreatedAt: time.Now(),
	}

	if err := s.repo.SaveRiskAssessment(ctx, assessment); err != nil {
		s.logger.Error("failed to save risk assessment", zap.Error(err))
	}

	s.logger.Info("fraud assessment",
		zap.String("user_id", userID.String()),
		zap.String("event", eventType),
		zap.Float64("score", score),
		zap.String("action", string(action)))

	return assessment, nil
}

// collectSignals gathers all cross-account correlation signals in a single DB query.
func (s *OnboardingFraudService) collectSignals(ctx context.Context, fingerprint, ip string) []entities.OnboardingRiskSignal {
	now := time.Now()
	accountsByFP, onboardingsFromIP, accountsByIP, err := s.repo.CountCorrelationSignals(
		ctx, fingerprint, ip,
		now.Add(-deviceCorrelationWindow),
		now.Add(-onboardingVelocityWindow),
		now.Add(-ipCorrelationWindow),
	)
	if err != nil {
		s.logger.Warn("correlation signal query failed", zap.Error(err))
		return nil
	}

	var signals []entities.OnboardingRiskSignal

	if fingerprint != "" && accountsByFP > maxAccountsPerDevice {
		score := float64(accountsByFP-maxAccountsPerDevice)/6.0 + 0.5
		if score > 1.0 {
			score = 1.0
		}
		signals = append(signals, entities.OnboardingRiskSignal{
			Type: "device_reuse", Score: score, Weight: signalWeights["device_reuse"],
			Detail: fmt.Sprintf("%d accounts from same device in 30d", accountsByFP),
		})
	}

	if ip != "" && onboardingsFromIP > maxOnboardingsPerIP {
		score := float64(onboardingsFromIP-maxOnboardingsPerIP)/5.0 + 0.4
		if score > 1.0 {
			score = 1.0
		}
		signals = append(signals, entities.OnboardingRiskSignal{
			Type: "ip_velocity", Score: score, Weight: signalWeights["ip_velocity"],
			Detail: fmt.Sprintf("%d onboardings from same IP in 24h", onboardingsFromIP),
		})
	}

	if ip != "" && accountsByIP > maxAccountsPerIP {
		score := float64(accountsByIP-maxAccountsPerIP)/10.0 + 0.3
		if score > 1.0 {
			score = 1.0
		}
		signals = append(signals, entities.OnboardingRiskSignal{
			Type: "ip_account_cluster", Score: score, Weight: signalWeights["ip_account_cluster"],
			Detail: fmt.Sprintf("%d accounts from same IP in 30d", accountsByIP),
		})
	}

	return signals
}

func scoreSignals(signals []entities.OnboardingRiskSignal) (float64, entities.FraudRiskLevel) {
	if len(signals) == 0 {
		return 0, entities.FraudRiskLow
	}

	var weightedSum, totalWeight float64
	for _, sig := range signals {
		weightedSum += sig.Score * sig.Weight
		totalWeight += sig.Weight
	}

	score := 0.0
	if totalWeight > 0 {
		score = weightedSum / totalWeight
	}
	if score > 1.0 {
		score = 1.0
	}

	// Multiple signals compound risk.
	if len(signals) >= 3 && score < 0.8 {
		score *= 1.3
		if score > 1.0 {
			score = 1.0
		}
	}

	level := entities.FraudRiskLow
	switch {
	case score >= thresholdBlock:
		level = entities.FraudRiskCritical
	case score >= thresholdManualReview:
		level = entities.FraudRiskHigh
	case score >= thresholdFlag:
		level = entities.FraudRiskMedium
	}

	return score, level
}

func determineAction(score float64) entities.FraudRiskAction {
	switch {
	case score >= thresholdBlock:
		return entities.FraudActionBlock
	case score >= thresholdManualReview:
		return entities.FraudActionManualReview
	case score >= thresholdDelayFunding:
		return entities.FraudActionDelayFunding
	case score >= thresholdFlag:
		return entities.FraudActionFlag
	default:
		return entities.FraudActionAllow
	}
}

func signalsToMap(signals []entities.OnboardingRiskSignal) map[string]interface{} {
	m := make(map[string]interface{}, len(signals))
	for _, sig := range signals {
		m[sig.Type] = map[string]interface{}{
			"score": sig.Score, "weight": sig.Weight, "detail": sig.Detail,
		}
	}
	return m
}
