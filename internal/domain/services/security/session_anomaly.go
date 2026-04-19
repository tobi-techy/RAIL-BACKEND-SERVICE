package security

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

const impossibleTravelSpeedKmH = 500.0

type SessionAnomalyService struct {
	repo   *repositories.SecurityFeaturesRepository
	logger *zap.Logger
}

func NewSessionAnomalyService(repo *repositories.SecurityFeaturesRepository, logger *zap.Logger) *SessionAnomalyService {
	return &SessionAnomalyService{repo: repo, logger: logger}
}

type SessionContext struct {
	UserID    uuid.UUID
	IP        string
	Country   string
	City      string
	UserAgent string
	Lat       float64
	Lon       float64
}

func (s *SessionAnomalyService) AnalyzeSession(ctx context.Context, current SessionContext) []entities.SessionAnomaly {
	var anomalies []entities.SessionAnomaly

	prevIP, prevCountry, _, prevUA, prevTime, err := s.repo.GetLastSession(ctx, current.UserID)
	if err != nil || prevIP == "" {
		// No previous session, record this one and return
		s.recordBaseline(ctx, current)
		return nil
	}

	// Impossible travel: different country within impossible timeframe
	elapsed := time.Since(prevTime)
	if prevCountry != "" && current.Country != "" && prevCountry != current.Country && elapsed < time.Hour {
		anomaly := s.createAnomaly(current, entities.AnomalyImpossibleTravel, "high", map[string]interface{}{
			"prev_country": prevCountry,
			"curr_country": current.Country,
			"elapsed_min":  elapsed.Minutes(),
		})
		anomalies = append(anomalies, anomaly)
	}

	// Concurrent sessions from different countries
	if prevCountry != "" && current.Country != "" && prevCountry != current.Country && elapsed < 5*time.Minute {
		anomaly := s.createAnomaly(current, entities.AnomalyConcurrentCountry, "critical", map[string]interface{}{
			"prev_country": prevCountry,
			"curr_country": current.Country,
		})
		anomalies = append(anomalies, anomaly)
	}

	// User-agent change
	if prevUA != "" && current.UserAgent != "" && prevUA != current.UserAgent && elapsed < 10*time.Minute {
		anomaly := s.createAnomaly(current, entities.AnomalyUserAgentChange, "medium", map[string]interface{}{
			"prev_ua": prevUA,
			"curr_ua": current.UserAgent,
		})
		anomalies = append(anomalies, anomaly)
	}

	// Store anomalies
	for i := range anomalies {
		if err := s.repo.CreateSessionAnomaly(ctx, &anomalies[i]); err != nil {
			s.logger.Error("Failed to store session anomaly", zap.Error(err))
		}
	}

	// Record current session as baseline for next check
	s.recordBaseline(ctx, current)

	return anomalies
}

func (s *SessionAnomalyService) createAnomaly(sc SessionContext, aType entities.AnomalyType, severity string, details map[string]interface{}) entities.SessionAnomaly {
	return entities.SessionAnomaly{
		ID:          uuid.New(),
		UserID:      sc.UserID,
		AnomalyType: aType,
		Severity:    severity,
		Details:     details,
		IPAddress:   sc.IP,
		Country:     sc.Country,
		City:        sc.City,
		CreatedAt:   time.Now(),
	}
}

func (s *SessionAnomalyService) recordBaseline(ctx context.Context, sc SessionContext) {
	baseline := &entities.SessionAnomaly{
		ID:          uuid.New(),
		UserID:      sc.UserID,
		AnomalyType: "session_baseline",
		Severity:    "info",
		Details:     map[string]interface{}{"user_agent": sc.UserAgent},
		IPAddress:   sc.IP,
		Country:     sc.Country,
		City:        sc.City,
		CreatedAt:   time.Now(),
	}
	s.repo.CreateSessionAnomaly(ctx, baseline)
}
