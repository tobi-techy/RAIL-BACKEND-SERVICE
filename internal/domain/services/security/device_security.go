package security

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

type DeviceSecurityService struct {
	deviceTracker *DeviceTrackingService
	eventLogger   *SecurityEventLogger
	logger        *zap.Logger
}

func NewDeviceSecurityService(dt *DeviceTrackingService, el *SecurityEventLogger, logger *zap.Logger) *DeviceSecurityService {
	return &DeviceSecurityService{deviceTracker: dt, eventLogger: el, logger: logger}
}

type DeviceValidationResult struct {
	TrustLevel  entities.DeviceTrustLevel `json:"trust_level"`
	IsKnown     bool                      `json:"is_known"`
	RiskScore   float64                   `json:"risk_score"`
	RiskFactors []string                  `json:"risk_factors"`
}

func (s *DeviceSecurityService) ValidateDevice(ctx context.Context, userID uuid.UUID, fingerprint, ip, userAgent string) (*DeviceValidationResult, error) {
	result := &DeviceValidationResult{
		TrustLevel:  entities.DeviceTrustNew,
		RiskFactors: []string{},
	}

	check, err := s.deviceTracker.CheckDevice(ctx, userID, fingerprint, ip)
	if err != nil {
		return nil, err
	}

	result.IsKnown = check.IsKnownDevice
	result.RiskScore = check.RiskScore
	result.RiskFactors = check.RiskFactors

	switch {
	case check.IsKnownDevice && check.IsTrusted:
		result.TrustLevel = entities.DeviceTrustTrusted
	case check.IsKnownDevice && check.Device != nil && time.Since(check.Device.LastUsedAt) > 30*24*time.Hour:
		result.TrustLevel = entities.DeviceTrustSuspicious
		result.RiskScore += 0.3
		result.RiskFactors = append(result.RiskFactors, "stale_device")
	case !check.IsKnownDevice:
		result.TrustLevel = entities.DeviceTrustNew
		s.eventLogger.LogNewDevice(ctx, userID, ip, userAgent, fingerprint, "")
	}

	if result.RiskScore > 0.6 {
		result.TrustLevel = entities.DeviceTrustSuspicious
		s.eventLogger.LogSuspiciousActivity(ctx, &userID, "device_risk", ip, result.RiskScore, result.RiskFactors)
	}

	return result, nil
}
