package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	sessionCacheTTL    = 30 * time.Second
	sessionCachePrefix = "session:"
)

// RedisClient interface for session caching
type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type Service struct {
	db     *sql.DB
	redis  RedisClient
	logger *zap.Logger
}

type Session struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"user_id"`
	TokenHash         string     `json:"token_hash"`
	RefreshTokenHash  string     `json:"refresh_token_hash"`
	IPAddress         string     `json:"ip_address"`
	UserAgent         string     `json:"user_agent"`
	DeviceFingerprint string     `json:"device_fingerprint"`
	Location          string     `json:"location"`
	IsActive          bool       `json:"is_active"`
	ExpiresAt         time.Time  `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
}

func NewService(db *sql.DB, redis RedisClient, logger *zap.Logger) *Service {
	return &Service{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// CreateSession creates a new user session
func (s *Service) CreateSession(ctx context.Context, userID uuid.UUID, accessToken, refreshToken, ipAddress, userAgent, deviceFingerprint, location string, expiresAt time.Time) (*Session, error) {
	if err := s.enforceConcurrentSessionLimit(ctx, userID, 5); err != nil {
		return nil, fmt.Errorf("failed to enforce session limit: %w", err)
	}

	tokenHash := s.hashToken(accessToken)
	refreshTokenHash := s.hashToken(refreshToken)

	session := &Session{
		ID:                uuid.New(),
		UserID:            userID,
		TokenHash:         tokenHash,
		RefreshTokenHash:  refreshTokenHash,
		IPAddress:         ipAddress,
		UserAgent:         userAgent,
		DeviceFingerprint: deviceFingerprint,
		Location:          location,
		IsActive:          true,
		ExpiresAt:         expiresAt,
		CreatedAt:         time.Now(),
	}

	query := `
		INSERT INTO sessions (id, user_id, token_hash, refresh_token_hash, ip_address, user_agent, device_fingerprint, location, is_active, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.TokenHash, session.RefreshTokenHash,
		session.IPAddress, session.UserAgent, session.DeviceFingerprint, session.Location,
		session.IsActive, session.ExpiresAt, session.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Cache the new session
	s.cacheSession(ctx, tokenHash, session)

	return session, nil
}

// ValidateSession validates a session by token hash with Redis caching
func (s *Service) ValidateSession(ctx context.Context, token string) (*Session, error) {
	tokenHash := s.hashToken(token)

	// Try cache first
	if session := s.getSessionFromCache(ctx, tokenHash); session != nil {
		go s.updateLastUsed(context.Background(), session.ID)
		return session, nil
	}

	// Cache miss - query database
	query := `
		SELECT id, user_id, token_hash, refresh_token_hash, ip_address, user_agent, 
		       device_fingerprint, location, is_active, expires_at, created_at, last_used_at
		FROM sessions 
		WHERE token_hash = $1 AND is_active = true AND expires_at > NOW()`

	session := &Session{}
	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(
		&session.ID, &session.UserID, &session.TokenHash, &session.RefreshTokenHash,
		&session.IPAddress, &session.UserAgent, &session.DeviceFingerprint, &session.Location,
		&session.IsActive, &session.ExpiresAt, &session.CreatedAt, &session.LastUsedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found or expired")
		}
		return nil, fmt.Errorf("failed to validate session: %w", err)
	}

	// Cache for future requests
	s.cacheSession(ctx, tokenHash, session)

	go s.updateLastUsed(context.Background(), session.ID)

	return session, nil
}

// InvalidateSession invalidates a specific session
func (s *Service) InvalidateSession(ctx context.Context, token string) error {
	tokenHash := s.hashToken(token)

	// Remove from cache
	s.invalidateSessionCache(ctx, tokenHash)

	query := `UPDATE sessions SET is_active = false WHERE token_hash = $1`

	_, err := s.db.ExecContext(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("failed to invalidate session: %w", err)
	}

	return nil
}

// InvalidateAllUserSessions invalidates all sessions for a user
func (s *Service) InvalidateAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	// Get all token hashes for user to invalidate cache
	rows, err := s.db.QueryContext(ctx,
		"SELECT token_hash FROM sessions WHERE user_id = $1 AND is_active = true", userID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tokenHash string
			if rows.Scan(&tokenHash) == nil {
				s.invalidateSessionCache(ctx, tokenHash)
			}
		}
	}

	query := `UPDATE sessions SET is_active = false WHERE user_id = $1 AND is_active = true`

	_, err = s.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to invalidate user sessions: %w", err)
	}

	return nil
}

// GetUserSessions returns active sessions for a user
func (s *Service) GetUserSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error) {
	query := `
		SELECT id, user_id, token_hash, refresh_token_hash, ip_address, user_agent,
		       device_fingerprint, location, is_active, expires_at, created_at, last_used_at
		FROM sessions 
		WHERE user_id = $1 AND is_active = true AND expires_at > NOW()
		ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		session := &Session{}
		err := rows.Scan(
			&session.ID, &session.UserID, &session.TokenHash, &session.RefreshTokenHash,
			&session.IPAddress, &session.UserAgent, &session.DeviceFingerprint, &session.Location,
			&session.IsActive, &session.ExpiresAt, &session.CreatedAt, &session.LastUsedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// CleanupExpiredSessions removes expired sessions
func (s *Service) CleanupExpiredSessions(ctx context.Context) error {
	query := `DELETE FROM sessions WHERE expires_at < NOW() OR (is_active = false AND updated_at < NOW() - INTERVAL '7 days')`

	result, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired sessions: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	s.logger.Info("Cleaned up expired sessions", zap.Int64("rows_affected", rowsAffected))

	return nil
}

func (s *Service) enforceConcurrentSessionLimit(ctx context.Context, userID uuid.UUID, limit int) error {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND is_active = true AND expires_at > NOW()",
		userID).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count sessions: %w", err)
	}

	if count >= limit {
		_, err = s.db.ExecContext(ctx, `
			UPDATE sessions 
			SET is_active = false 
			WHERE id = (
				SELECT id FROM sessions 
				WHERE user_id = $1 AND is_active = true 
				ORDER BY created_at ASC 
				LIMIT 1
			)`, userID)
		if err != nil {
			return fmt.Errorf("failed to invalidate oldest session: %w", err)
		}
	}

	return nil
}

func (s *Service) updateLastUsed(ctx context.Context, sessionID uuid.UUID) {
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET last_used_at = NOW() WHERE id = $1", sessionID)
	if err != nil {
		s.logger.Warn("Failed to update session last used", zap.Error(err))
	}
}

func (s *Service) hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// Cache helpers
func (s *Service) cacheSession(ctx context.Context, tokenHash string, session *Session) {
	if s.redis == nil {
		return
	}
	data, err := json.Marshal(session)
	if err != nil {
		s.logger.Warn("Failed to marshal session for cache", zap.Error(err))
		return
	}
	if err := s.redis.Set(ctx, sessionCachePrefix+tokenHash, data, sessionCacheTTL).Err(); err != nil {
		s.logger.Warn("Failed to cache session", zap.Error(err))
	}
}

func (s *Service) getSessionFromCache(ctx context.Context, tokenHash string) *Session {
	if s.redis == nil {
		return nil
	}
	data, err := s.redis.Get(ctx, sessionCachePrefix+tokenHash).Bytes()
	if err != nil {
		return nil // Cache miss
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil
	}
	// Verify not expired
	if session.ExpiresAt.Before(time.Now()) || !session.IsActive {
		s.invalidateSessionCache(ctx, tokenHash)
		return nil
	}
	return &session
}

func (s *Service) invalidateSessionCache(ctx context.Context, tokenHash string) {
	if s.redis == nil {
		return
	}
	s.redis.Del(ctx, sessionCachePrefix+tokenHash)
}
