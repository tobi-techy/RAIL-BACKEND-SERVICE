package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"rail_service/pkg/auth"
)

// DeviceSessionRepository implements auth.SessionStore
type DeviceSessionRepository struct {
	db *sqlx.DB
}

// NewDeviceSessionRepository creates a new device session repository
func NewDeviceSessionRepository(db *sqlx.DB) *DeviceSessionRepository {
	return &DeviceSessionRepository{db: db}
}

// CreateSession creates a new device-bound session
func (r *DeviceSessionRepository) CreateSession(ctx context.Context, session *auth.DeviceSession) error {
	query := `
		INSERT INTO user_sessions (
			id, user_id, device_id, session_id, binding_hash, 
			ip_address, user_agent, device_fingerprint, is_active, 
			created_at, expires_at, last_used_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err := r.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.DeviceID, session.SessionID, session.BindingHash,
		session.IPAddress, session.UserAgent, session.DeviceFingerprint, session.IsActive,
		session.CreatedAt, session.ExpiresAt, session.LastUsedAt)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by session ID
func (r *DeviceSessionRepository) GetSession(ctx context.Context, sessionID string) (*auth.DeviceSession, error) {
	query := `
		SELECT id, user_id, device_id, session_id, binding_hash, 
		       ip_address, user_agent, device_fingerprint, is_active,
		       created_at, expires_at, last_used_at, revoked_at
		FROM user_sessions 
		WHERE session_id = $1`

	var session auth.DeviceSession
	var ipAddress, userAgent, deviceFingerprint sql.NullString
	var revokedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, sessionID).Scan(
		&session.ID, &session.UserID, &session.DeviceID, &session.SessionID, &session.BindingHash,
		&ipAddress, &userAgent, &deviceFingerprint, &session.IsActive,
		&session.CreatedAt, &session.ExpiresAt, &session.LastUsedAt, &revokedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	session.IPAddress = ipAddress.String
	session.UserAgent = userAgent.String
	session.DeviceFingerprint = deviceFingerprint.String
	if revokedAt.Valid {
		session.RevokedAt = &revokedAt.Time
	}

	return &session, nil
}

// GetSessionsByUser retrieves all active sessions for a user
func (r *DeviceSessionRepository) GetSessionsByUser(ctx context.Context, userID uuid.UUID) ([]*auth.DeviceSession, error) {
	query := `
		SELECT id, user_id, device_id, session_id, binding_hash,
		       ip_address, user_agent, device_fingerprint, is_active,
		       created_at, expires_at, last_used_at, revoked_at
		FROM user_sessions 
		WHERE user_id = $1 AND is_active = true AND expires_at > NOW()
		ORDER BY last_used_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*auth.DeviceSession
	for rows.Next() {
		var session auth.DeviceSession
		var ipAddress, userAgent, deviceFingerprint sql.NullString
		var revokedAt sql.NullTime

		err := rows.Scan(
			&session.ID, &session.UserID, &session.DeviceID, &session.SessionID, &session.BindingHash,
			&ipAddress, &userAgent, &deviceFingerprint, &session.IsActive,
			&session.CreatedAt, &session.ExpiresAt, &session.LastUsedAt, &revokedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		session.IPAddress = ipAddress.String
		session.UserAgent = userAgent.String
		session.DeviceFingerprint = deviceFingerprint.String
		if revokedAt.Valid {
			session.RevokedAt = &revokedAt.Time
		}

		sessions = append(sessions, &session)
	}

	return sessions, nil
}

// CountActiveSessions counts active sessions for a user
func (r *DeviceSessionRepository) CountActiveSessions(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM user_sessions WHERE user_id = $1 AND is_active = true AND expires_at > NOW()`

	var count int
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count sessions: %w", err)
	}
	return count, nil
}

// GetOldestSession gets the oldest active session for a user
func (r *DeviceSessionRepository) GetOldestSession(ctx context.Context, userID uuid.UUID) (*auth.DeviceSession, error) {
	query := `
		SELECT id, user_id, device_id, session_id, binding_hash,
		       ip_address, user_agent, device_fingerprint, is_active,
		       created_at, expires_at, last_used_at, revoked_at
		FROM user_sessions 
		WHERE user_id = $1 AND is_active = true AND expires_at > NOW()
		ORDER BY created_at ASC
		LIMIT 1`

	var session auth.DeviceSession
	var ipAddress, userAgent, deviceFingerprint sql.NullString
	var revokedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&session.ID, &session.UserID, &session.DeviceID, &session.SessionID, &session.BindingHash,
		&ipAddress, &userAgent, &deviceFingerprint, &session.IsActive,
		&session.CreatedAt, &session.ExpiresAt, &session.LastUsedAt, &revokedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get oldest session: %w", err)
	}

	session.IPAddress = ipAddress.String
	session.UserAgent = userAgent.String
	session.DeviceFingerprint = deviceFingerprint.String
	if revokedAt.Valid {
		session.RevokedAt = &revokedAt.Time
	}

	return &session, nil
}

// RevokeSession revokes a specific session
func (r *DeviceSessionRepository) RevokeSession(ctx context.Context, sessionID string) error {
	query := `UPDATE user_sessions SET is_active = false, revoked_at = NOW() WHERE session_id = $1`

	_, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// RevokeAllUserSessions revokes all sessions for a user
func (r *DeviceSessionRepository) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE user_sessions SET is_active = false, revoked_at = NOW() WHERE user_id = $1 AND is_active = true`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke all user sessions: %w", err)
	}
	return nil
}

// UpdateLastUsed updates the last used timestamp
func (r *DeviceSessionRepository) UpdateLastUsed(ctx context.Context, sessionID string) error {
	query := `UPDATE user_sessions SET last_used_at = NOW() WHERE session_id = $1`

	_, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update last used: %w", err)
	}
	return nil
}

// DeviceBindingAuditRepository handles audit logging for device binding
type DeviceBindingAuditRepository struct {
	db *sqlx.DB
}

// NewDeviceBindingAuditRepository creates a new audit repository
func NewDeviceBindingAuditRepository(db *sqlx.DB) *DeviceBindingAuditRepository {
	return &DeviceBindingAuditRepository{db: db}
}

// LogDeviceBinding logs a device binding audit entry
func (r *DeviceBindingAuditRepository) LogDeviceBinding(ctx context.Context, entry *auth.DeviceBindingAuditEntry) error {
	query := `
		INSERT INTO device_binding_audit (
			user_id, session_id, device_id, action, ip_address, user_agent, risk_score, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	var metadataJSON []byte
	if entry.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(entry.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	_, err := r.db.ExecContext(ctx, query,
		entry.UserID, entry.SessionID, entry.DeviceID, entry.Action,
		entry.IPAddress, entry.UserAgent, entry.RiskScore, metadataJSON)

	if err != nil {
		return fmt.Errorf("failed to log device binding audit: %w", err)
	}
	return nil
}

// GetAuditLogsByUser retrieves audit logs for a user
func (r *DeviceBindingAuditRepository) GetAuditLogsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*auth.DeviceBindingAuditEntry, error) {
	query := `
		SELECT user_id, session_id, device_id, action, ip_address, user_agent, risk_score, metadata, created_at
		FROM device_binding_audit 
		WHERE user_id = $1 
		ORDER BY created_at DESC 
		LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}
	defer rows.Close()

	var entries []*auth.DeviceBindingAuditEntry
	for rows.Next() {
		var entry auth.DeviceBindingAuditEntry
		var sessionID sql.NullString
		var ipAddress, userAgent sql.NullString
		var metadataJSON []byte
		var createdAt time.Time

		err := rows.Scan(&entry.UserID, &sessionID, &entry.DeviceID, &entry.Action,
			&ipAddress, &userAgent, &entry.RiskScore, &metadataJSON, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit entry: %w", err)
		}

		if sessionID.Valid {
			id, _ := uuid.Parse(sessionID.String)
			entry.SessionID = &id
		}
		entry.IPAddress = ipAddress.String
		entry.UserAgent = userAgent.String

		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &entry.Metadata)
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}
