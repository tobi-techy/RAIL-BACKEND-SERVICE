package security

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type EventType string
type Severity string

const (
	EventLoginSuccess       EventType = "login_success"
	EventLoginFailed        EventType = "login_failed"
	EventAccountLocked      EventType = "account_locked"
	EventPasswordChanged    EventType = "password_changed"
	EventMFAEnabled         EventType = "mfa_enabled"
	EventMFADisabled        EventType = "mfa_disabled"
	EventNewDevice          EventType = "new_device"
	EventSuspiciousActivity EventType = "suspicious_activity"
	EventWithdrawalRequest  EventType = "withdrawal_request"
	EventWithdrawalConfirm  EventType = "withdrawal_confirmed"
	EventIPWhitelistAdd     EventType = "ip_whitelist_add"
	EventIPWhitelistRemove  EventType = "ip_whitelist_remove"
	EventSessionInvalidated EventType = "session_invalidated"
	EventAPIKeyCreated      EventType = "api_key_created"
	EventAPIKeyRevoked      EventType = "api_key_revoked"

	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityWarning  Severity = "warning"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type SecurityEvent struct {
	ID                uuid.UUID
	UserID            *uuid.UUID
	EventType         EventType
	Severity          Severity
	IPAddress         string
	UserAgent         string
	DeviceFingerprint string
	Metadata          map[string]interface{}
	CreatedAt         time.Time
}

type SecurityEventLogger struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewSecurityEventLogger(db *sql.DB, logger *zap.Logger) *SecurityEventLogger {
	return &SecurityEventLogger{db: db, logger: logger}
}

// Log records a security event
func (s *SecurityEventLogger) Log(ctx context.Context, event *SecurityEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO security_events (id, user_id, event_type, severity, ip_address, user_agent, device_fingerprint, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := s.db.ExecContext(ctx, query,
		event.ID, event.UserID, event.EventType, event.Severity,
		event.IPAddress, event.UserAgent, event.DeviceFingerprint,
		event.Metadata, event.CreatedAt)

	if err != nil {
		s.logger.Error("Failed to log security event",
			zap.Error(err),
			zap.String("event_type", string(event.EventType)))
		return err
	}

	// Also log to structured logger for real-time monitoring
	s.logger.Info("Security event",
		zap.String("event_type", string(event.EventType)),
		zap.String("severity", string(event.Severity)),
		zap.Any("user_id", event.UserID),
		zap.String("ip", event.IPAddress))

	return nil
}

// LogLoginSuccess logs a successful login
func (s *SecurityEventLogger) LogLoginSuccess(ctx context.Context, userID uuid.UUID, ip, userAgent, fingerprint string) {
	s.Log(ctx, &SecurityEvent{
		UserID:            &userID,
		EventType:         EventLoginSuccess,
		Severity:          SeverityInfo,
		IPAddress:         ip,
		UserAgent:         userAgent,
		DeviceFingerprint: fingerprint,
	})
}

// LogLoginFailed logs a failed login attempt
func (s *SecurityEventLogger) LogLoginFailed(ctx context.Context, email, ip, userAgent, reason string) {
	s.Log(ctx, &SecurityEvent{
		EventType: EventLoginFailed,
		Severity:  SeverityWarning,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata: map[string]interface{}{
			"email":  email,
			"reason": reason,
		},
	})
}

// LogAccountLocked logs an account lockout
func (s *SecurityEventLogger) LogAccountLocked(ctx context.Context, email, ip string, duration time.Duration) {
	s.Log(ctx, &SecurityEvent{
		EventType: EventAccountLocked,
		Severity:  SeverityCritical,
		IPAddress: ip,
		Metadata: map[string]interface{}{
			"email":         email,
			"lock_duration": duration.String(),
		},
	})
}

// LogNewDevice logs a new device detection
func (s *SecurityEventLogger) LogNewDevice(ctx context.Context, userID uuid.UUID, ip, userAgent, fingerprint, deviceName string) {
	s.Log(ctx, &SecurityEvent{
		UserID:            &userID,
		EventType:         EventNewDevice,
		Severity:          SeverityWarning,
		IPAddress:         ip,
		UserAgent:         userAgent,
		DeviceFingerprint: fingerprint,
		Metadata: map[string]interface{}{
			"device_name": deviceName,
		},
	})
}

// LogSuspiciousActivity logs suspicious activity
func (s *SecurityEventLogger) LogSuspiciousActivity(ctx context.Context, userID *uuid.UUID, activityType, ip string, riskScore float64, factors []string) {
	s.Log(ctx, &SecurityEvent{
		UserID:    userID,
		EventType: EventSuspiciousActivity,
		Severity:  SeverityCritical,
		IPAddress: ip,
		Metadata: map[string]interface{}{
			"activity_type": activityType,
			"risk_score":    riskScore,
			"risk_factors":  factors,
		},
	})
}

// LogWithdrawalRequest logs a withdrawal request
func (s *SecurityEventLogger) LogWithdrawalRequest(ctx context.Context, userID uuid.UUID, amount, destAddress, ip string, requiresMFA bool) {
	s.Log(ctx, &SecurityEvent{
		UserID:    &userID,
		EventType: EventWithdrawalRequest,
		Severity:  SeverityInfo,
		IPAddress: ip,
		Metadata: map[string]interface{}{
			"amount":       amount,
			"destination":  destAddress,
			"requires_mfa": requiresMFA,
		},
	})
}

// GetUserSecurityEvents retrieves security events for a user
func (s *SecurityEventLogger) GetUserSecurityEvents(ctx context.Context, userID uuid.UUID, limit int) ([]*SecurityEvent, error) {
	query := `
		SELECT id, user_id, event_type, severity, ip_address, user_agent, device_fingerprint, metadata, created_at
		FROM security_events
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := s.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*SecurityEvent
	for rows.Next() {
		e := &SecurityEvent{}
		err := rows.Scan(&e.ID, &e.UserID, &e.EventType, &e.Severity, &e.IPAddress, &e.UserAgent, &e.DeviceFingerprint, &e.Metadata, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	return events, nil
}

// GetRecentCriticalEvents retrieves recent critical security events
func (s *SecurityEventLogger) GetRecentCriticalEvents(ctx context.Context, since time.Time, limit int) ([]*SecurityEvent, error) {
	query := `
		SELECT id, user_id, event_type, severity, ip_address, user_agent, device_fingerprint, metadata, created_at
		FROM security_events
		WHERE severity = 'critical' AND created_at > $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := s.db.QueryContext(ctx, query, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*SecurityEvent
	for rows.Next() {
		e := &SecurityEvent{}
		err := rows.Scan(&e.ID, &e.UserID, &e.EventType, &e.Severity, &e.IPAddress, &e.UserAgent, &e.DeviceFingerprint, &e.Metadata, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	return events, nil
}
