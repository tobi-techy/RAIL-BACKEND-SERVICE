package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingFraudRepository handles cross-account device/network correlation.
type OnboardingFraudRepository interface {
	// RecordDeviceAccountLink stores a device→account association for cross-account correlation.
	RecordDeviceAccountLink(ctx context.Context, link *entities.DeviceAccountLink) error

	// CountCorrelationSignals returns all three correlation counts in a single DB round-trip:
	// accountsByFingerprint, onboardingsFromIP, accountsByIP.
	CountCorrelationSignals(ctx context.Context, fingerprint, ip string, deviceSince, ipVelocitySince, ipClusterSince time.Time) (accountsByFingerprint, onboardingsFromIP, accountsByIP int, err error)

	// SaveRiskAssessment persists a risk assessment for audit.
	SaveRiskAssessment(ctx context.Context, assessment *entities.OnboardingRiskAssessment) error

	// GetLatestAssessment returns the most recent risk assessment for a user.
	GetLatestAssessment(ctx context.Context, userID uuid.UUID) (*entities.OnboardingRiskAssessment, error)
}
