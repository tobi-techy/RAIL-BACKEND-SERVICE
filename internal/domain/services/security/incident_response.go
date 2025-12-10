package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type IncidentResponseService struct {
	db            *sql.DB
	redis         *redis.Client
	logger        *zap.Logger
	notifier      IncidentNotifier
	eventLogger   *SecurityEventLogger
}

type IncidentNotifier interface {
	NotifySecurityTeam(ctx context.Context, incident *SecurityIncident) error
	NotifyUser(ctx context.Context, userID uuid.UUID, message string) error
}

type SecurityIncident struct {
	ID                uuid.UUID
	Type              IncidentType
	Severity          Severity // Use existing Severity type
	Status            IncidentStatus
	AffectedUserID    *uuid.UUID
	AffectedUsersCount int
	Description       string
	DetectionMethod   string
	Indicators        map[string]interface{}
	ResponseActions   []ResponseAction
	AssignedTo        string
	ResolvedAt        *time.Time
	ResolutionNotes   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type IncidentType string

const (
	IncidentTypeBreachAttempt    IncidentType = "breach_attempt"
	IncidentTypeAccountTakeover  IncidentType = "account_takeover"
	IncidentTypeFraud            IncidentType = "fraud"
	IncidentTypeDataLeak         IncidentType = "data_leak"
	IncidentTypeDDoS             IncidentType = "ddos"
	IncidentTypeMalware          IncidentType = "malware"
	IncidentTypeUnauthorizedAccess IncidentType = "unauthorized_access"
	IncidentTypeSuspiciousActivity IncidentType = "suspicious_activity"
)

type IncidentStatus string

const (
	StatusOpen          IncidentStatus = "open"
	StatusInvestigating IncidentStatus = "investigating"
	StatusContained     IncidentStatus = "contained"
	StatusResolved      IncidentStatus = "resolved"
	StatusFalsePositive IncidentStatus = "false_positive"
)

type ResponseAction struct {
	Type        string
	Details     map[string]interface{}
	PerformedBy string
	PerformedAt time.Time
}

func NewIncidentResponseService(db *sql.DB, redis *redis.Client, logger *zap.Logger, notifier IncidentNotifier, eventLogger *SecurityEventLogger) *IncidentResponseService {
	return &IncidentResponseService{
		db:          db,
		redis:       redis,
		logger:      logger,
		notifier:    notifier,
		eventLogger: eventLogger,
	}
}

// CreateIncident creates a new security incident
func (s *IncidentResponseService) CreateIncident(ctx context.Context, incident *SecurityIncident) error {
	incident.ID = uuid.New()
	incident.Status = StatusOpen
	incident.CreatedAt = time.Now()
	incident.UpdatedAt = time.Now()

	indicatorsJSON, _ := json.Marshal(incident.Indicators)
	actionsJSON, _ := json.Marshal(incident.ResponseActions)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO security_incidents 
		(id, incident_type, severity, status, affected_user_id, affected_users_count, description, detection_method, indicators, response_actions, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		incident.ID, incident.Type, incident.Severity, incident.Status,
		incident.AffectedUserID, incident.AffectedUsersCount, incident.Description,
		incident.DetectionMethod, indicatorsJSON, actionsJSON,
		incident.CreatedAt, incident.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create incident: %w", err)
	}

	// Notify security team for high/critical incidents
	if incident.Severity == SeverityHigh || incident.Severity == SeverityCritical {
		if s.notifier != nil {
			s.notifier.NotifySecurityTeam(ctx, incident)
		}
	}

	s.logger.Warn("Security incident created",
		zap.String("incident_id", incident.ID.String()),
		zap.String("type", string(incident.Type)),
		zap.String("severity", string(incident.Severity)))

	return nil
}

// DetectBreachAttempt analyzes patterns to detect potential breaches
func (s *IncidentResponseService) DetectBreachAttempt(ctx context.Context) error {
	// Check for multiple failed logins from same IP
	rows, err := s.db.QueryContext(ctx, `
		SELECT ip_address, COUNT(*) as attempts, COUNT(DISTINCT identifier) as unique_users
		FROM login_attempts 
		WHERE success = false AND created_at > NOW() - INTERVAL '1 hour'
		GROUP BY ip_address
		HAVING COUNT(*) > 20 OR COUNT(DISTINCT identifier) > 5`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var ip string
		var attempts, uniqueUsers int
		if rows.Scan(&ip, &attempts, &uniqueUsers) != nil {
			continue
		}

		// Create incident
		incident := &SecurityIncident{
			Type:            IncidentTypeBreachAttempt,
			Severity:        s.determineSeverity(attempts, uniqueUsers),
			Description:     fmt.Sprintf("Potential credential stuffing attack from IP %s: %d attempts against %d users", ip, attempts, uniqueUsers),
			DetectionMethod: "automated",
			Indicators: map[string]interface{}{
				"ip_address":   ip,
				"attempts":     attempts,
				"unique_users": uniqueUsers,
			},
		}

		s.CreateIncident(ctx, incident)

		// Auto-respond: block IP temporarily
		s.executeResponse(ctx, incident.ID, "block_ip", map[string]interface{}{"ip": ip, "duration": "1h"}, "system")
	}

	return nil
}

// DetectAccountTakeover checks for signs of account compromise
func (s *IncidentResponseService) DetectAccountTakeover(ctx context.Context, userID uuid.UUID, signals []FraudSignal) error {
	// Calculate risk from signals
	var riskScore float64
	for _, signal := range signals {
		riskScore += signal.Value
	}

	if riskScore < 0.7 {
		return nil
	}

	// Check for password change + new device + unusual location
	var recentPasswordChange bool
	s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM password_history WHERE user_id = $1 AND created_at > NOW() - INTERVAL '24 hours')`,
		userID).Scan(&recentPasswordChange)

	if recentPasswordChange && riskScore > 0.8 {
		incident := &SecurityIncident{
			Type:           IncidentTypeAccountTakeover,
			Severity:       SeverityHigh,
			AffectedUserID: &userID,
			Description:    "Potential account takeover detected: recent password change with suspicious activity",
			DetectionMethod: "automated",
			Indicators: map[string]interface{}{
				"risk_score":             riskScore,
				"recent_password_change": true,
				"signals":                signals,
			},
		}

		if err := s.CreateIncident(ctx, incident); err != nil {
			return err
		}

		// Auto-respond: lock account and notify user
		s.executeResponse(ctx, incident.ID, "lock_account", map[string]interface{}{"user_id": userID.String()}, "system")
		s.executeResponse(ctx, incident.ID, "notify_user", map[string]interface{}{"user_id": userID.String()}, "system")
		s.executeResponse(ctx, incident.ID, "revoke_sessions", map[string]interface{}{"user_id": userID.String()}, "system")
	}

	return nil
}

// ExecutePlaybook runs automated response actions based on incident type
func (s *IncidentResponseService) ExecutePlaybook(ctx context.Context, incidentID uuid.UUID) error {
	incident, err := s.GetIncident(ctx, incidentID)
	if err != nil {
		return err
	}

	// Update status to investigating
	s.UpdateIncidentStatus(ctx, incidentID, StatusInvestigating, "")

	switch incident.Type {
	case IncidentTypeBreachAttempt:
		return s.executeBreachPlaybook(ctx, incident)
	case IncidentTypeAccountTakeover:
		return s.executeAccountTakeoverPlaybook(ctx, incident)
	case IncidentTypeFraud:
		return s.executeFraudPlaybook(ctx, incident)
	default:
		return s.executeDefaultPlaybook(ctx, incident)
	}
}

func (s *IncidentResponseService) executeBreachPlaybook(ctx context.Context, incident *SecurityIncident) error {
	// 1. Block suspicious IPs
	if ip, ok := incident.Indicators["ip_address"].(string); ok {
		s.executeResponse(ctx, incident.ID, "block_ip", map[string]interface{}{"ip": ip, "duration": "24h"}, "system")
	}

	// 2. Enable enhanced monitoring
	s.executeResponse(ctx, incident.ID, "enable_monitoring", map[string]interface{}{"level": "enhanced"}, "system")

	// 3. Alert security team
	s.executeResponse(ctx, incident.ID, "alert_security_team", map[string]interface{}{"priority": "high"}, "system")

	return nil
}

func (s *IncidentResponseService) executeAccountTakeoverPlaybook(ctx context.Context, incident *SecurityIncident) error {
	if incident.AffectedUserID == nil {
		return nil
	}

	userID := *incident.AffectedUserID

	// 1. Lock account
	s.executeResponse(ctx, incident.ID, "lock_account", map[string]interface{}{"user_id": userID.String()}, "system")

	// 2. Revoke all sessions
	s.executeResponse(ctx, incident.ID, "revoke_sessions", map[string]interface{}{"user_id": userID.String()}, "system")

	// 3. Force password reset
	s.executeResponse(ctx, incident.ID, "force_password_reset", map[string]interface{}{"user_id": userID.String()}, "system")

	// 4. Notify user via alternate channel
	s.executeResponse(ctx, incident.ID, "notify_user_alternate", map[string]interface{}{"user_id": userID.String()}, "system")

	// 5. Freeze withdrawals
	s.executeResponse(ctx, incident.ID, "freeze_withdrawals", map[string]interface{}{"user_id": userID.String()}, "system")

	return nil
}

func (s *IncidentResponseService) executeFraudPlaybook(ctx context.Context, incident *SecurityIncident) error {
	if incident.AffectedUserID == nil {
		return nil
	}

	userID := *incident.AffectedUserID

	// 1. Freeze account transactions
	s.executeResponse(ctx, incident.ID, "freeze_transactions", map[string]interface{}{"user_id": userID.String()}, "system")

	// 2. Flag for manual review
	s.executeResponse(ctx, incident.ID, "flag_for_review", map[string]interface{}{"user_id": userID.String()}, "system")

	// 3. Collect evidence
	s.executeResponse(ctx, incident.ID, "collect_evidence", map[string]interface{}{"user_id": userID.String()}, "system")

	return nil
}

func (s *IncidentResponseService) executeDefaultPlaybook(ctx context.Context, incident *SecurityIncident) error {
	// Default: alert and monitor
	s.executeResponse(ctx, incident.ID, "alert_security_team", map[string]interface{}{}, "system")
	s.executeResponse(ctx, incident.ID, "enable_monitoring", map[string]interface{}{"level": "standard"}, "system")
	return nil
}

// executeResponse performs a response action and logs it
func (s *IncidentResponseService) executeResponse(ctx context.Context, incidentID uuid.UUID, actionType string, details map[string]interface{}, performedBy string) error {
	// Log the action
	detailsJSON, _ := json.Marshal(details)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_response_log (incident_id, action_type, action_details, performed_by)
		VALUES ($1, $2, $3, $4)`,
		incidentID, actionType, detailsJSON, performedBy)
	if err != nil {
		s.logger.Error("Failed to log response action", zap.Error(err))
	}

	// Execute the action
	switch actionType {
	case "lock_account":
		if userIDStr, ok := details["user_id"].(string); ok {
			userID, _ := uuid.Parse(userIDStr)
			s.db.ExecContext(ctx, "UPDATE users SET is_locked = true, locked_at = NOW() WHERE id = $1", userID)
		}

	case "revoke_sessions":
		if userIDStr, ok := details["user_id"].(string); ok {
			userID, _ := uuid.Parse(userIDStr)
			s.db.ExecContext(ctx, "UPDATE sessions SET is_active = false WHERE user_id = $1", userID)
			// Also add to Redis blacklist
			s.redis.Set(ctx, fmt.Sprintf("user_blacklist:%s", userID.String()), time.Now().Unix(), 24*time.Hour)
		}

	case "force_password_reset":
		if userIDStr, ok := details["user_id"].(string); ok {
			userID, _ := uuid.Parse(userIDStr)
			s.db.ExecContext(ctx, "UPDATE users SET require_password_change = true WHERE id = $1", userID)
		}

	case "freeze_withdrawals":
		if userIDStr, ok := details["user_id"].(string); ok {
			userID, _ := uuid.Parse(userIDStr)
			s.db.ExecContext(ctx, "UPDATE users SET withdrawals_frozen = true, withdrawals_frozen_at = NOW() WHERE id = $1", userID)
		}

	case "block_ip":
		if ip, ok := details["ip"].(string); ok {
			duration := "1h"
			if d, ok := details["duration"].(string); ok {
				duration = d
			}
			ttl, _ := time.ParseDuration(duration)
			s.redis.Set(ctx, fmt.Sprintf("blocked_ip:%s", ip), "1", ttl)
		}

	case "notify_user":
		if userIDStr, ok := details["user_id"].(string); ok && s.notifier != nil {
			userID, _ := uuid.Parse(userIDStr)
			s.notifier.NotifyUser(ctx, userID, "Security alert: Suspicious activity detected on your account. Please verify your recent activity.")
		}
	}

	s.logger.Info("Response action executed",
		zap.String("incident_id", incidentID.String()),
		zap.String("action", actionType))

	return nil
}

// GetIncident retrieves an incident by ID
func (s *IncidentResponseService) GetIncident(ctx context.Context, incidentID uuid.UUID) (*SecurityIncident, error) {
	incident := &SecurityIncident{}
	var indicatorsJSON, actionsJSON []byte

	err := s.db.QueryRowContext(ctx, `
		SELECT id, incident_type, severity, status, affected_user_id, affected_users_count, 
		       description, detection_method, indicators, response_actions, assigned_to,
		       resolved_at, resolution_notes, created_at, updated_at
		FROM security_incidents WHERE id = $1`,
		incidentID).Scan(
		&incident.ID, &incident.Type, &incident.Severity, &incident.Status,
		&incident.AffectedUserID, &incident.AffectedUsersCount, &incident.Description,
		&incident.DetectionMethod, &indicatorsJSON, &actionsJSON, &incident.AssignedTo,
		&incident.ResolvedAt, &incident.ResolutionNotes, &incident.CreatedAt, &incident.UpdatedAt)
	if err != nil {
		return nil, err
	}

	json.Unmarshal(indicatorsJSON, &incident.Indicators)
	json.Unmarshal(actionsJSON, &incident.ResponseActions)

	return incident, nil
}

// UpdateIncidentStatus updates incident status
func (s *IncidentResponseService) UpdateIncidentStatus(ctx context.Context, incidentID uuid.UUID, status IncidentStatus, notes string) error {
	var resolvedAt *time.Time
	if status == StatusResolved || status == StatusFalsePositive {
		now := time.Now()
		resolvedAt = &now
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE security_incidents SET status = $1, resolution_notes = $2, resolved_at = $3, updated_at = NOW()
		WHERE id = $4`,
		status, notes, resolvedAt, incidentID)
	return err
}

// GetOpenIncidents returns all open incidents
func (s *IncidentResponseService) GetOpenIncidents(ctx context.Context) ([]*SecurityIncident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_type, severity, status, affected_user_id, description, created_at
		FROM security_incidents 
		WHERE status IN ('open', 'investigating', 'contained')
		ORDER BY 
			CASE severity 
				WHEN 'critical' THEN 1 
				WHEN 'high' THEN 2 
				WHEN 'medium' THEN 3 
				ELSE 4 
			END,
			created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []*SecurityIncident
	for rows.Next() {
		incident := &SecurityIncident{}
		if rows.Scan(&incident.ID, &incident.Type, &incident.Severity, &incident.Status,
			&incident.AffectedUserID, &incident.Description, &incident.CreatedAt) == nil {
			incidents = append(incidents, incident)
		}
	}

	return incidents, nil
}

// GetSecurityDashboard returns metrics for security monitoring
func (s *IncidentResponseService) GetSecurityDashboard(ctx context.Context) (map[string]interface{}, error) {
	dashboard := make(map[string]interface{})

	// Open incidents by severity
	var criticalCount, highCount, mediumCount, lowCount int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_incidents WHERE status IN ('open', 'investigating') AND severity = 'critical'").Scan(&criticalCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_incidents WHERE status IN ('open', 'investigating') AND severity = 'high'").Scan(&highCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_incidents WHERE status IN ('open', 'investigating') AND severity = 'medium'").Scan(&mediumCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_incidents WHERE status IN ('open', 'investigating') AND severity = 'low'").Scan(&lowCount)

	dashboard["open_incidents"] = map[string]int{
		"critical": criticalCount,
		"high":     highCount,
		"medium":   mediumCount,
		"low":      lowCount,
	}

	// Failed logins last 24h
	var failedLogins int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM login_attempts WHERE success = false AND created_at > NOW() - INTERVAL '24 hours'").Scan(&failedLogins)
	dashboard["failed_logins_24h"] = failedLogins

	// Blocked IPs count
	blockedIPs, _ := s.redis.Keys(ctx, "blocked_ip:*").Result()
	dashboard["blocked_ips"] = len(blockedIPs)

	// High fraud score users
	var highFraudUsers int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE fraud_score > 0.7").Scan(&highFraudUsers)
	dashboard["high_fraud_users"] = highFraudUsers

	// Recent security events
	var recentEvents int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_events WHERE created_at > NOW() - INTERVAL '1 hour'").Scan(&recentEvents)
	dashboard["security_events_1h"] = recentEvents

	return dashboard, nil
}

func (s *IncidentResponseService) determineSeverity(attempts, uniqueUsers int) Severity {
	if attempts > 100 || uniqueUsers > 20 {
		return SeverityCritical
	}
	if attempts > 50 || uniqueUsers > 10 {
		return SeverityHigh
	}
	if attempts > 20 || uniqueUsers > 5 {
		return SeverityMedium
	}
	return SeverityLow
}
